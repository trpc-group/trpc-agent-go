export function normalizeActKind(kind) {
  const value = `${kind || ""}`.trim();
  switch (value.toLowerCase()) {
    case "key":
    case "key/press":
    case "keyboard.press":
    case "press/key":
    case "presskey":
      return "press";
    default:
      return value;
  }
}
