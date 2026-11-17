const link = document.getElementById("install-link");
link?.addEventListener("click", () => {
  const feedback = document.getElementById("install-feedback");
  if (feedback) feedback.hidden = false;
});
