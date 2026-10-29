import { defineConfig, loadEnv, createLogger } from "vite";
import tailwindcss from "@tailwindcss/vite";
import { gsx, devFallback } from "@gsxhq/vite-plugin-gsx";

export default defineConfig(({ command, mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const goPort = env.GO_PORT || "7777";
  const vitePort = parseInt(env.VITE_PORT || "5173", 10);
  // `gsx dev` injects GSX_DEV_UPSTREAM; standalone vite and builds fall back to GO_PORT.
  const upstream = process.env.GSX_DEV_UPSTREAM ?? `http://localhost:${goPort}`;
  const fallback = devFallback({ target: upstream });

  const logger = createLogger();
  const baseError = logger.error;
  logger.error = (msg, opts) => {
    if (typeof msg === "string" && msg.includes("http proxy error")) return;
    baseError(msg, opts);
  };

  return {
    clearScreen: false,
    base: command === "serve" ? "/__vite/" : "/static/",
    publicDir: false,
    customLogger: logger,
    plugins: [tailwindcss(), gsx(), fallback.plugin],
    server: {
      port: vitePort,
      strictPort: true,
      proxy: {
        "^(?!/__vite/|/__dev).*": {
          target: upstream,
          changeOrigin: true,
          configure: fallback.configureProxy,
        },
      },
    },
    build: {
      manifest: true,
      outDir: "examples/lab/dist",
      emptyOutDir: true,
      rollupOptions: { input: ["examples/lab/web/main.js"] },
    },
  };
});
