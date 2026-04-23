import { defineConfig } from "astro/config";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  site: "https://disk-space-analyser.keremorenli.com",
  vite: {
    plugins: [tailwindcss()],
  },
});
