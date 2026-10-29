import "./style.css";
// Order matters: the extension reads the global `htmx` at eval time.
import "./htmx.js";
import "htmx.org/dist/ext/hx-sse.js";
import "./direct.js";
import "./meter.js";
