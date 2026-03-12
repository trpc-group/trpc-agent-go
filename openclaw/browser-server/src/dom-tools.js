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
  let ref = node.getAttribute(refAttr);
  if (ref) {
    return ref;
  }
  ref = `e${Math.random().toString(36).slice(2, 8)}`;
  node.setAttribute(refAttr, ref);
  return ref;
}

export function snapshotDOM() {
  const localRefAttr = "data-openclaw-ref";
  const localInteractiveSelector = [
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
  function localIsVisible(node) {
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
  function localTextFor(node) {
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
  function localRoleFor(node) {
    return (
      node.getAttribute("role") ||
      node.tagName.toLowerCase()
    );
  }
  function localEnsureRef(node) {
    let ref = node.getAttribute(localRefAttr);
    if (ref) {
      return ref;
    }
    ref = `e${Math.random().toString(36).slice(2, 8)}`;
    node.setAttribute(localRefAttr, ref);
    return ref;
  }
  const nodes = Array.from(
    document.querySelectorAll(localInteractiveSelector)
  )
    .filter(localIsVisible)
    .slice(0, 200);
  const items = nodes.map((node) => ({
    ref: localEnsureRef(node),
    role: localRoleFor(node),
    text: localTextFor(node),
    tag: node.tagName.toLowerCase(),
    type: node.getAttribute("type") || "",
    disabled: Boolean(node.disabled)
  }));
  const lines = [
    `Page: ${document.title || ""}`,
    `URL: ${window.location.href}`
  ];
  for (const item of items) {
    const label = item.text ? ` "${item.text}"` : "";
    const kind = item.type ? ` type=${item.type}` : "";
    const disabled = item.disabled ? " disabled" : "";
    lines.push(`[${item.ref}] ${item.role}${kind}${disabled}${label}`);
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
      return snapshotDOM();
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
