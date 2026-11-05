// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';

// The Skylands documentation, published at docs.skylands.ai. It looks like
// the citadel's web client: the same island mark beside the name, the same
// fonts, and the same colors, which src/styles/theme.css maps onto
// Starlight's.
export default defineConfig({
	site: 'https://docs.skylands.ai',
	// There's one section so far, so the site opens on it.
	redirects: {
		'/': '/getting-started/installation/',
	},
	integrations: [
		starlight({
			title: 'Skylands',
			description: 'Run, watch, and approve coding agents across your machines, shown as a 3D world of floating islands.',
			logo: { src: './src/assets/mark.svg', alt: '' },
			favicon: '/favicon.svg',
			social: [{ icon: 'github', label: 'GitHub', href: 'https://github.com/skylands-ai/skylands' }],
			editLink: { baseUrl: 'https://github.com/skylands-ai/skylands/edit/main/docsite/' },
			customCss: [
				'@fontsource/barlow/400.css',
				'@fontsource/barlow/500.css',
				'@fontsource/barlow/600.css',
				'@fontsource/barlow/700.css',
				'@fontsource/jetbrains-mono/400.css',
				'@fontsource/jetbrains-mono/500.css',
				'@fontsource/sora/700.css',
				'./src/styles/theme.css',
			],
			sidebar: [
				{
					label: 'Getting started',
					items: [{ label: 'Installation', slug: 'getting-started/installation' }],
				},
			],
		}),
	],
});
