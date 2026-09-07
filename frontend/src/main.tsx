import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import LogWindow from "./screens/LogWindow";
import { initTheme } from "./design/theme";
import "./design/tokens.css";

initTheme();

const root = document.getElementById("root");
if (!root) {
  throw new Error("root element missing");
}

ReactDOM.createRoot(root).render(
  <React.StrictMode>
    {new URLSearchParams(window.location.search).get("window") === "logs" ? <LogWindow /> : <App />}
  </React.StrictMode>,
);
