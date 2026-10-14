// A short message at the bottom of the screen, optionally with a button
// such as Undo.

export const toastState = $state({
  text: "",
  action: null as null | { label: string; run: () => void },
  visible: false,
});

let timer = 0;

export function toast(text: string, action?: { label: string; run: () => void }) {
  clearTimeout(timer);
  toastState.text = text;
  toastState.action = action ?? null;
  toastState.visible = true;
  timer = window.setTimeout(hideToast, action ? 8000 : 3000);
}

export function hideToast() {
  clearTimeout(timer);
  toastState.visible = false;
}
