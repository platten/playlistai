// Request illustrations only: this page does not run a model or generate tracks.
const examples = {
  ambient: { lines: ["Ambient electronica.", "Relaxing, but not sleepy."], fields: [["Genre", "Ambient electronica"], ["Mood", "Relaxing"], ["Avoid", "Sleepy"]] },
  artist: { lines: ["ODESZA.", "Build me something with songs like that."], fields: [["Reference", "ODESZA · artist"], ["Direction", "Similar tracks"], ["Intent", "Discover something new"]] },
  journey: { lines: ["20th-century classical.", "End with Miles Davis."], fields: [["Start", "Classical · 20th century"], ["Destination", "Miles Davis · artist"], ["Shape", "A musical journey"]] },
};
for (const button of document.querySelectorAll("[data-example]")) {
  button.addEventListener("click", () => {
    const example = examples[button.dataset.example];
    const description = document.getElementById("example-description");
    description.replaceChildren(document.createTextNode(example.lines[0]), document.createElement("br"), document.createTextNode(example.lines[1]));
    const summary = document.getElementById("example-summary");
    summary.replaceChildren(...example.fields.map(([label, value]) => {
      const row = document.createElement("div");
      const term = document.createElement("dt");
      const detail = document.createElement("dd");
      term.textContent = label;
      detail.textContent = value;
      row.append(term, detail);
      return row;
    }));
    document.querySelectorAll("[data-example]").forEach(item => item.setAttribute("aria-pressed", String(item === button)));
  });
}
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
