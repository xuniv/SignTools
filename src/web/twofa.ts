import { request } from "./lib/api";
const form = document.getElementById("twofa-form");
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
          feedback.textContent = "Code submitted. Signing will continue shortly.";
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
