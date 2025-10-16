// @ts-check
import { defineConfig } from 'astro/config';
import react from '@astrojs/react';
import tailwind from '@astrojs/tailwind';

// https://astro.build/config
export default defineConfig({
			// Site and base for GitHub Pages deployment
			site: 'https://rolldone.github.io',
			base: '/make-sync/',
				// Integrasi React dan Tailwind CSS
				integrations: [
					react(),
					tailwind({
						config: {
							// gunakan file tailwind.config.cjs di root
						},
					}),
				],
});
