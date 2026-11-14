document.body.addEventListener("htmx:beforeRequest", function (e) {
  if (document.hidden && e.detail.elt.classList.contains("poll")) {
    e.preventDefault();
  }
});
document.body.addEventListener("htmx:responseError", function (e) {
  var flash = document.getElementById("flash");
  if (flash && e.detail.xhr.status !== 401) {
    flash.textContent = "Request failed: " + e.detail.xhr.status;
    flash.className = "error";
  }
});
