const refAttr = "data-openclaw-ref";
const interactiveSelector = [
  "a[href]",
  "button",
  "input",
  "select",
  "textarea",
  "[role='button']",
  "[role='link']",
  "[contenteditable='true']",
  "[tabindex]"
].join(",");
const meaningfulSelector = [
  "main",
  "section",
  "article",
  "nav",
  "form",
  "h1",
  "h2",
  "h3",
  "h4",
  "h5",
  "h6",
  "p",
  "ul",
  "ol",
  "li",
  "label",
  "a[href]",
  "button",
  "input",
  "select",
  "textarea",
  "[role]",
  "[contenteditable='true']"
].join(",");

export function snapshotDOM(options = {}) {
  const snapshotRefAttr = "data-openclaw-ref";
  const snapshotInteractiveSelector = [
    "a[href]",
    "button",
    "input",
    "select",
    "textarea",
    "[role='button']",
    "[role='link']",
    "[contenteditable='true']",
    "[tabindex]"
  ].join(",");
  const snapshotMeaningfulSelector = [
    "main",
    "section",
    "article",
    "nav",
    "form",
    "h1",
    "h2",
    "h3",
    "h4",
    "h5",
    "h6",
    "p",
    "ul",
    "ol",
    "li",
    "label",
    "a[href]",
    "button",
    "input",
    "select",
    "textarea",
    "[role]",
    "[contenteditable='true']"
  ].join(",");
  function isVisible(node) {
    if (!node || !(node instanceof Element)) {
      return false;
    }
    const style = window.getComputedStyle(node);
    if (style.visibility === "hidden" || style.display === "none") {
      return false;
    }
    const rect = node.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  }
  function textFor(node) {
    const parts = [
      node.getAttribute("aria-label"),
      node.getAttribute("placeholder"),
      node.innerText,
      node.textContent,
      node.value
    ];
    for (const raw of parts) {
      const text = `${raw || ""}`.trim().replace(/\s+/g, " ");
      if (text) {
        return text;
      }
    }
    return "";
  }
  function roleFor(node) {
    return (
      node.getAttribute("role") ||
      node.tagName.toLowerCase()
    );
  }
  function ensureRef(node) {
    let ref = node.getAttribute(snapshotRefAttr);
    if (ref) {
      return ref;
    }
    ref = `e${Math.random().toString(36).slice(2, 8)}`;
    node.setAttribute(snapshotRefAttr, ref);
    return ref;
  }
  function matchesSelector(node, selector) {
    return typeof node.matches === "function" && node.matches(selector);
  }
  function isInteractive(node) {
    return matchesSelector(node, snapshotInteractiveSelector);
  }
  function isMeaningful(node, interactiveOnly) {
    if (isInteractive(node)) {
      return true;
    }
    if (interactiveOnly) {
      return false;
    }
    return (
      matchesSelector(node, snapshotMeaningfulSelector) ||
      textFor(node) !== ""
    );
  }
  function snapshotLine(item, compact) {
    const indent = "  ".repeat(item.depth || 0);
    const ref = item.ref ? `[${item.ref}] ` : "";
    const label = item.text ? ` "${item.text}"` : "";
    if (compact) {
      return `${indent}${ref}${item.role}${label}`;
    }
    const tag = item.tag && item.tag !== item.role
      ? ` tag=${item.tag}`
      : "";
    const kind = item.type ? ` type=${item.type}` : "";
    const disabled = item.disabled ? " disabled" : "";
    return `${indent}${ref}${item.role}${tag}${kind}${disabled}${label}`;
  }

  const selector = `${options?.selector || ""}`.trim();
  const limit = Math.max(0, Number(options?.limit) || 0) || 200;
  const interactiveOnly = options?.interactive !== false;
  const compact = options?.compact === true;
  const maxDepth = Number.isFinite(Number(options?.depth)) &&
    Number(options?.depth) >= 0
    ? Number(options?.depth)
    : undefined;
  const root = selector
    ? document.querySelector(selector)
    : document.body || document.documentElement;
  if (!root) {
    throw new Error(`Snapshot selector not found: ${selector}`);
  }

  const items = [];
  const stack = [{
    node: root,
    depth: 0
  }];
  while (stack.length > 0 && items.length < limit) {
    const current = stack.pop();
    const node = current?.node;
    if (!node || !isVisible(node)) {
      continue;
    }
    const include = isMeaningful(node, interactiveOnly);
    if (include) {
      if (maxDepth === undefined || current.depth <= maxDepth) {
        items.push({
          ref: isInteractive(node) ? ensureRef(node) : "",
          role: roleFor(node),
          text: textFor(node),
          tag: node.tagName.toLowerCase(),
          type: node.getAttribute("type") || "",
          disabled: Boolean(node.disabled),
          depth: current.depth,
          interactive: isInteractive(node)
        });
      }
    }
    const nextDepth = include ? current.depth + 1 : current.depth;
    if (maxDepth !== undefined && nextDepth > maxDepth) {
      continue;
    }
    const children = Array.from(node.children || []);
    for (let index = children.length - 1; index >= 0; index -= 1) {
      stack.push({
        node: children[index],
        depth: nextDepth
      });
    }
  }

  const lines = [
    `Page: ${document.title || ""}`,
    `URL: ${window.location.href}`
  ];
  if (selector) {
    lines.push(`Selector: ${selector}`);
  }
  for (const item of items) {
    lines.push(snapshotLine(item, compact));
  }
  return {
    title: document.title || "",
    url: window.location.href,
    items,
    text: lines.join("\n")
  };
}

function queryByRef(ref) {
  return document.querySelector(`[${refAttr}="${ref}"]`);
}

function setValueAndEvents(node, text) {
  node.focus();
  if ("value" in node) {
    node.value = text;
  }
  node.dispatchEvent(new Event("input", { bubbles: true }));
  node.dispatchEvent(new Event("change", { bubbles: true }));
}

export function executeRelayCommand(command) {
  const { action, args = {} } = command;
  switch (action) {
    case "snapshot":
      return snapshotDOM(args);
    case "click": {
      const node = queryByRef(args.ref);
      if (!node) {
        throw new Error(`Unknown ref: ${args.ref}`);
      }
      node.click();
      return { ok: true };
    }
    case "hover": {
      const node = queryByRef(args.ref);
      if (!node) {
        throw new Error(`Unknown ref: ${args.ref}`);
      }
      node.dispatchEvent(new MouseEvent("mouseover", { bubbles: true }));
      return { ok: true };
    }
    case "type": {
      const node = queryByRef(args.ref);
      if (!node) {
        throw new Error(`Unknown ref: ${args.ref}`);
      }
      setValueAndEvents(node, `${args.text || ""}`);
      if (args.submit && node.form) {
        node.form.requestSubmit();
      }
      return { ok: true };
    }
    case "select": {
      const node = queryByRef(args.ref);
      if (!node) {
        throw new Error(`Unknown ref: ${args.ref}`);
      }
      if (!(node instanceof HTMLSelectElement)) {
        throw new Error(`Ref ${args.ref} is not a select element`);
      }
      const values = new Set(args.values || []);
      for (const option of Array.from(node.options)) {
        option.selected = values.has(option.value);
      }
      node.dispatchEvent(new Event("input", { bubbles: true }));
      node.dispatchEvent(new Event("change", { bubbles: true }));
      return { ok: true };
    }
    case "fill": {
      for (const field of args.fields || []) {
        const node = queryByRef(field.ref);
        if (!node) {
          throw new Error(`Unknown ref: ${field.ref}`);
        }
        setValueAndEvents(node, `${field.text || ""}`);
      }
      return { ok: true };
    }
    case "press": {
      const node = document.activeElement || document.body;
      const key = `${args.key || ""}`;
      node.dispatchEvent(new KeyboardEvent("keydown", {
        key,
        bubbles: true
      }));
      node.dispatchEvent(new KeyboardEvent("keyup", {
        key,
        bubbles: true
      }));
      return { ok: true };
    }
    case "wait": {
      return { ok: true };
    }
    default:
      throw new Error(`Unsupported relay action: ${action}`);
  }
}
