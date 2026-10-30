import "./style.css";
// The meter wraps fetch, so it must load before Datastar opens the stream.
import { watchPatches } from "./meter.js";
import "./vendor/datastar-1.0.4.js";

watchPatches();
