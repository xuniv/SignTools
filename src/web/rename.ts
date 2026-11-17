import { request } from "./lib/api";
const form = document.getElementById("rename-form");
if (form instanceof HTMLFormElement)
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    const feedback = document.getElementById("form-feedback");
    const button = form.querySelector("button");
    if (button) button.disabled = true;
    void request(location.pathname, { method: "POST", body: new FormData(form) })
      .then(() => {
        if (feedback) {
          feedback.hidden = false;
          feedback.textContent = "Name saved. You can close this page.";
        }
      })
      .catch((error) => {
        if (feedback) {
          feedback.hidden = false;
          feedback.textContent = error instanceof Error ? error.message : String(error);
        }
      })
      .finally(() => {
        if (button) button.disabled = false;
      });
  });
