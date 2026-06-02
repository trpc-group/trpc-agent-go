#!/usr/bin/env python3
"""Python AST Parser for trpc-agent-go knowledge system.

Usage: python python_parser.py <file_path> [module_path] [extract_imports]
Output: JSON with nodes and edges to stdout.
"""
import ast
import json
import sys
from typing import List, Dict, Any, Optional, Tuple


class PythonASTParser(ast.NodeVisitor):
    """Python AST parser that extracts code entities and relationships."""

    def __init__(self, file_path: str, module_path: str, source_lines: List[str], extract_imports: bool = True):
        self.file_path = file_path
        self.module_path = module_path
        self.source_lines = source_lines
        self.extract_imports = extract_imports
        self.nodes: List[Dict] = []
        self.edges: List[Dict] = []
        self.current_class: Optional[str] = None
        self.current_function: Optional[str] = None
        self.import_map: Dict[str, str] = {}
        self.imports: List[str] = []
        self.chunk_index = 0

    def _get_code_with_comments(self, node) -> Tuple[str, int]:
        """Extract source code including preceding comments and decorators."""
        start_line = node.lineno
        if hasattr(node, 'decorator_list') and node.decorator_list:
            start_line = node.decorator_list[0].lineno

        comment_start = start_line
        for i in range(start_line - 2, max(0, start_line - 12), -1):
            if i < len(self.source_lines):
                line = self.source_lines[i].strip()
                if line.startswith('#'):
                    comment_start = i + 1
                elif line == '':
                    if comment_start == start_line:
                        break
                else:
                    break

        end_line = node.end_lineno or node.lineno
        actual_start = comment_start - 1
        actual_end = end_line

        if actual_start < 0:
            actual_start = 0
        if actual_end > len(self.source_lines):
            actual_end = len(self.source_lines)

        return '\n'.join(self.source_lines[actual_start:actual_end]), comment_start

    def _build_class_skeleton(self, node: ast.ClassDef) -> str:
        """Build skeleton: declaration + docstring + class vars + method signatures."""
        lines = []

        for d in node.decorator_list:
            lines.append("@{}".format(ast.unparse(d)))

        bases = [ast.unparse(b) for b in node.bases]
        keywords = [ast.unparse(k) for k in node.keywords]
        all_args = bases + keywords
        decl = "class {}".format(node.name)
        if all_args:
            decl += "({})".format(", ".join(all_args))
        decl += ":"
        lines.append(decl)

        has_content = False
        for i, child in enumerate(node.body):
            if (i == 0 and isinstance(child, ast.Expr)
                    and isinstance(getattr(child, 'value', None), ast.Constant)
                    and isinstance(child.value.value, str)):
                lines.append('    """{}"""'.format(child.value.value))
                has_content = True
                continue

            if isinstance(child, (ast.Assign, ast.AnnAssign)):
                lines.append("    {}".format(ast.unparse(child)))
                has_content = True
            elif isinstance(child, (ast.FunctionDef, ast.AsyncFunctionDef)):
                for d in child.decorator_list:
                    lines.append("    @{}".format(ast.unparse(d)))
                params = self._build_func_params(child.args)
                returns = " -> {}".format(ast.unparse(child.returns)) if child.returns else ""
                async_prefix = "async " if isinstance(child, ast.AsyncFunctionDef) else ""
                lines.append("    {}def {}({}){}: ...".format(
                    async_prefix, child.name, params, returns))
                has_content = True

        if not has_content:
            lines.append("    pass")

        return "\n".join(lines)

    def _build_func_params(self, args: ast.arguments) -> str:
        """Build full parameter list string from ast.arguments."""
        parts = []
        defaults_offset = len(args.args) - len(args.defaults)

        for i, arg in enumerate(args.args):
            param = arg.arg
            if arg.annotation:
                param += ": {}".format(ast.unparse(arg.annotation))
            if i >= defaults_offset:
                param += "={}".format(ast.unparse(args.defaults[i - defaults_offset]))
            parts.append(param)

        if args.vararg:
            param = "*{}".format(args.vararg.arg)
            if args.vararg.annotation:
                param += ": {}".format(ast.unparse(args.vararg.annotation))
            parts.append(param)
        elif args.kwonlyargs:
            parts.append("*")

        for i, arg in enumerate(args.kwonlyargs):
            param = arg.arg
            if arg.annotation:
                param += ": {}".format(ast.unparse(arg.annotation))
            if i < len(args.kw_defaults) and args.kw_defaults[i] is not None:
                param += "={}".format(ast.unparse(args.kw_defaults[i]))
            parts.append(param)

        if args.kwarg:
            param = "**{}".format(args.kwarg.arg)
            if args.kwarg.annotation:
                param += ": {}".format(ast.unparse(args.kwarg.annotation))
            parts.append(param)

        return ", ".join(parts)

    def visit_Import(self, node: ast.Import):
        """Handle: import foo, import foo as bar"""
        for alias in node.names:
            local_name = alias.asname or alias.name
            self.import_map[local_name] = alias.name
            # Keep the imports list for node metadata, but do not emit IMPORTS
            # edges: their targets are external modules without parsed nodes,
            # which would create dangling graph vertices. This aligns with the
            # Go reader, which also does not emit IMPORTS edges.
            if self.extract_imports:
                self.imports.append(alias.name)

    def visit_ImportFrom(self, node: ast.ImportFrom):
        """Handle: from foo import bar, from foo import bar as baz"""
        module = node.module or ""
        for alias in node.names:
            if alias.name == "*":
                import_target = module
            else:
                import_target = "{}.{}".format(module, alias.name) if module else alias.name
            local_name = alias.asname or alias.name
            self.import_map[local_name] = import_target
            # Keep the imports list for node metadata, but do not emit IMPORTS
            # edges (see visit_Import for rationale).
            if self.extract_imports:
                self.imports.append(import_target)

    def visit_ClassDef(self, node: ast.ClassDef):
        """Extract class definition."""
        is_interface = any(
            (isinstance(b, ast.Name) and b.id in ("Protocol", "ABC")) or
            (isinstance(b, ast.Attribute) and b.attr in ("Protocol", "ABC"))
            for b in node.bases
        )
        entity_type = "Interface" if is_interface else "Class"
        bases = [ast.unparse(b) for b in node.bases]
        signature = "class {}".format(node.name)
        if bases:
            signature += "({})".format(", ".join(bases))

        _, line_start = self._get_code_with_comments(node)
        code = self._build_class_skeleton(node)

        node_id = "{}.{}".format(self.module_path, node.name)
        self.nodes.append({
            "id": node_id,
            "name": node.name,
            "full_name": node_id,
            "type": entity_type,
            "package": self.module_path,
            "imports": self.imports,
            "signature": signature,
            "comment": ast.get_docstring(node) or "",
            "code": code,
            "file_path": self.file_path,
            "line_start": line_start,
            "line_end": node.end_lineno or node.lineno,
            "metadata": {
                "exported": not node.name.startswith("_"),
                "code_chunk_index": self.chunk_index,
            }
        })
        self.chunk_index += 1

        for base in node.bases:
            base_name = ast.unparse(base)
            resolved = self._resolve_symbol(base_name)
            rel_type = "IMPLEMENTS" if base_name in ("Protocol", "ABC") else "INHERITS"
            self.edges.append({"from_id": node_id, "to_id": resolved, "type": rel_type})

        old_class = self.current_class
        self.current_class = node.name
        self.generic_visit(node)
        self.current_class = old_class

    def visit_FunctionDef(self, node: ast.FunctionDef):
        self._visit_function(node, is_async=False)

    def visit_AsyncFunctionDef(self, node: ast.AsyncFunctionDef):
        self._visit_function(node, is_async=True)

    def _visit_function(self, node, is_async: bool):
        """Extract function/method definition."""
        is_method = self.current_class is not None
        entity_type = "Method" if is_method else "Function"

        params = []
        for arg in node.args.args:
            param = arg.arg
            if arg.annotation:
                param += ": {}".format(ast.unparse(arg.annotation))
            params.append(param)
        returns = " -> {}".format(ast.unparse(node.returns)) if node.returns else ""
        async_prefix = "async " if is_async else ""
        signature = "{}def {}({}){}".format(async_prefix, node.name, ", ".join(params), returns)

        if is_method:
            node_id = "{}.{}.{}".format(self.module_path, self.current_class, node.name)
        else:
            node_id = "{}.{}".format(self.module_path, node.name)

        code, line_start = self._get_code_with_comments(node)

        self.nodes.append({
            "id": node_id,
            "name": node.name,
            "full_name": node_id,
            "type": entity_type,
            "package": self.module_path,
            "imports": self.imports,
            "signature": signature,
            "comment": ast.get_docstring(node) or "",
            "code": code,
            "file_path": self.file_path,
            "line_start": line_start,
            "line_end": node.end_lineno or node.lineno,
            "metadata": {
                "exported": not node.name.startswith("_"),
                "code_chunk_index": self.chunk_index,
            }
        })
        self.chunk_index += 1

        if is_method:
            class_id = "{}.{}".format(self.module_path, self.current_class)
            self.edges.append({"from_id": class_id, "to_id": node_id, "type": "METHOD"})

        old_function = self.current_function
        self.current_function = node_id
        self.generic_visit(node)
        self.current_function = old_function

    def visit_Call(self, node: ast.Call):
        """Extract function call relationships."""
        if self.current_function:
            callee = self._get_call_target(node)
            if callee and not self._is_builtin(callee):
                resolved = self._resolve_symbol(callee)
                self.edges.append({
                    "from_id": self.current_function,
                    "to_id": resolved,
                    "type": "CALLS"
                })
        self.generic_visit(node)

    def visit_Assign(self, node: ast.Assign):
        """Extract module-level variable assignments."""
        if self.current_class is None and self.current_function is None:
            for target in node.targets:
                if isinstance(target, ast.Name):
                    node_id = "{}.{}".format(self.module_path, target.id)
                    code, line_start = self._get_code_with_comments(node)
                    self.nodes.append({
                        "id": node_id,
                        "name": target.id,
                        "full_name": node_id,
                        "type": "Variable",
                        "package": self.module_path,
                        "imports": self.imports,
                        "signature": "{} = ...".format(target.id),
                        "comment": "",
                        "code": code,
                        "file_path": self.file_path,
                        "line_start": line_start,
                        "line_end": node.end_lineno or node.lineno,
                        "metadata": {
                            "exported": not target.id.startswith("_"),
                            "code_chunk_index": self.chunk_index,
                        }
                    })
                    self.chunk_index += 1
        self.generic_visit(node)

    def visit_AnnAssign(self, node: ast.AnnAssign):
        """Extract annotated assignments: x: int = 1"""
        if self.current_class is None and self.current_function is None:
            if isinstance(node.target, ast.Name):
                node_id = "{}.{}".format(self.module_path, node.target.id)
                type_ann = ast.unparse(node.annotation) if node.annotation else ""
                code, line_start = self._get_code_with_comments(node)
                self.nodes.append({
                    "id": node_id,
                    "name": node.target.id,
                    "full_name": node_id,
                    "type": "Variable",
                    "package": self.module_path,
                    "imports": self.imports,
                    "signature": "{}: {}".format(node.target.id, type_ann),
                    "comment": "",
                    "code": code,
                    "file_path": self.file_path,
                    "line_start": line_start,
                    "line_end": node.end_lineno or node.lineno,
                    "metadata": {
                        "exported": not node.target.id.startswith("_"),
                        "code_chunk_index": self.chunk_index,
                    }
                })
                self.chunk_index += 1
        self.generic_visit(node)

    def _get_call_target(self, node: ast.Call) -> Optional[str]:
        """Get the call target name."""
        if isinstance(node.func, ast.Name):
            return node.func.id
        elif isinstance(node.func, ast.Attribute):
            if isinstance(node.func.value, ast.Name):
                return "{}.{}".format(node.func.value.id, node.func.attr)
            return node.func.attr
        return None

    def _resolve_symbol(self, name: str) -> str:
        """Resolve a symbol using import_map."""
        if "." in name:
            parts = name.split(".", 1)
            if parts[0] in self.import_map:
                return "{}.{}".format(self.import_map[parts[0]], parts[1])
        if name in self.import_map:
            return self.import_map[name]
        return name

    def _is_builtin(self, name: str) -> bool:
        """Check if name is a Python builtin."""
        builtins = {
            "len", "range", "print", "str", "int", "float", "bool", "list",
            "dict", "set", "tuple", "type", "isinstance", "issubclass",
            "hasattr", "getattr", "setattr", "delattr", "callable", "iter",
            "next", "enumerate", "zip", "map", "filter", "sorted", "reversed",
            "min", "max", "sum", "abs", "round", "open", "repr", "id", "hash",
            "super", "object", "classmethod", "staticmethod", "property",
        }
        return name.split(".")[0] in builtins


def parse_file(file_path: str, module_path: str, extract_imports: bool = True) -> Dict[str, Any]:
    """Parse a Python file and return nodes and edges."""
    with open(file_path, "r", encoding="utf-8") as f:
        source = f.read()

    source_lines = source.splitlines()
    tree = ast.parse(source, filename=file_path)
    parser = PythonASTParser(file_path, module_path, source_lines, extract_imports)
    parser.visit(tree)

    return {
        "nodes": parser.nodes,
        "edges": parser.edges,
    }


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: python python_parser.py <file_path> [module_path] [extract_imports]", file=sys.stderr)
        sys.exit(1)
    file_path = sys.argv[1]
    module_path = sys.argv[2] if len(sys.argv) > 2 else ""
    extract_imports = True
    if len(sys.argv) > 3:
        extract_imports = sys.argv[3].lower() not in ("false", "0", "no")
    result = parse_file(file_path, module_path, extract_imports)
    print(json.dumps(result, ensure_ascii=False))
