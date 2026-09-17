/// <reference types="vitest/config" />
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

/**
 * The Content Security Policy, carried over from the Electron shell's `app://` protocol handler
 * with one required change: `connect-src` is `'self'` rather than `'none'`.
 *
 * The Wails runtime reaches Go through requests to the page's own origin — `wails://localhost` on
 * macOS, `http://wails.localhost` on Windows — so `'none'` would block every command the app makes.
 * `'self'` is still a closed door to the network: nothing may contact a remote host from the
 * renderer, which is the property the original policy was protecting.
 *
 * `'unsafe-eval'` stays for the API client's Postman-style scripts (`lib/api/sandbox.ts` builds a
 * `new Function`). Everything else is unchanged from 2.x.
 */
const CSP = [
  "default-src 'self'",
  "script-src 'self' 'unsafe-eval'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data:",
  "font-src 'self'",
  "worker-src 'self'",
  "connect-src 'self'",
  "object-src 'none'",
  "base-uri 'none'",
  "form-action 'none'",
  "frame-src 'none'",
].join("; ");

/**
 * Injects the policy into production builds only.
 *
 * Not in dev: Vite's dev server needs a websocket to localhost for HMR, which `connect-src 'self'`
 * would refuse, and a policy that only exists in the build nobody runs locally is a policy that
 * gets discovered in a release.
 */
function contentSecurityPolicy(): Plugin {
  return {
    name: "codeflow-csp",
    apply: "build",
    transformIndexHtml(html) {
      return {
        html,
        tags: [
          {
            tag: "meta",
            attrs: { "http-equiv": "Content-Security-Policy", content: CSP },
            injectTo: "head-prepend",
          },
        ],
      };
    },
  };
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss(), contentSecurityPolicy()],

  // Assets are referenced relatively so the built renderer works under the shell's `app://`
  // protocol handler. Absolute `/assets/…` paths would resolve against the protocol root and 404
  // in a packaged build while working fine in dev — the worst kind of difference to debug.
  base: "./",

  server: {
    // The shell's dev mode points at this exact URL, so the port is fixed. Failing fast on a
    // collision beats the shell silently loading someone else's dev server.
    port: 1420,
    strictPort: true,
  },

  build: {
    // Monaco is its own chunk now, loaded when an editor is first opened, so this is back to being
    // a signal instead of an excuse. It was 4000 while everything shipped as one 21 MB bundle --
    // a threshold nothing could ever cross, which trains everyone to ignore it. 1000 is above the
    // app's own code and below anything that would be worth splitting again.
    //
    // No `manualChunks`: Rollup already hoists what several lazy entries share into one chunk, and
    // hand-written chunk boundaries would go stale the moment an import moves.
    chunkSizeWarningLimit: 1000,
  },

  // Vitest reads this same config, so tests resolve modules exactly the way the app does. That is
  // the whole reason it is here rather than `node --test`: `renderer/src` is full of extensionless
  // relative imports, which Node's resolver rejects and Vite's accepts.
  test: {
    // The default, stated rather than implied: nothing here touches a DOM. Component tests would
    // need `jsdom` and `@testing-library/react`, and none of the three are installed — this covers
    // pure logic, and says so.
    environment: "node",

    // Tests live beside what they test. The i18n check is the exception: it reads
    // `translations.ts` as text rather than importing it, so it sits in `scripts/`.
    include: ["src/**/*.test.ts", "scripts/**/*.test.mjs"],
  },
});
