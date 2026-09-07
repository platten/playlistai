const toggle = document.querySelector(".menu-toggle");
const navigation = document.getElementById("navigation");
function closeMenu() {
  navigation.classList.remove("open");
  toggle.setAttribute("aria-expanded", "false");
  toggle.setAttribute("aria-label", "Open navigation");
}
toggle.addEventListener("click", () => {
  const open = navigation.classList.toggle("open");
  toggle.setAttribute("aria-expanded", String(open));
  toggle.setAttribute("aria-label", open ? "Close navigation" : "Open navigation");
});
navigation.querySelectorAll("a").forEach(link => link.addEventListener("click", closeMenu));
document.addEventListener("keydown", event => {
  if (event.key === "Escape" && navigation.classList.contains("open")) { closeMenu(); toggle.focus(); }
});
window.matchMedia("(min-width: 801px)").addEventListener("change", closeMenu);
