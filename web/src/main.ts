import { mount } from "svelte";
import "./app.css";
import App from "./App.svelte";
import { registerServiceWorker } from "./lib/app.svelte";

mount(App, { target: document.getElementById("app")! });
registerServiceWorker();
