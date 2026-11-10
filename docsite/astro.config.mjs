// @ts-check
import { defineConfig } from 'astro/config';
import { visit } from 'unist-util-visit';
import { readFileSync } from 'node:fs';
import starlight from '@astrojs/starlight';
import { fileURLToPath } from 'node:url';

const pkgPath = fileURLToPath(new URL('./package.json', import.meta.url));


function remarkReplaceVersion() {
  return (tree) => {
    // Read fresh on every Markdown compilation
    const { version } = JSON.parse(readFileSync(pkgPath, 'utf-8'));

    visit(tree, ['code', 'inlineCode'], (node) => {
      node.value = node.value.replaceAll('__VERSION__', version);
    });
  };
}

// The Skylands documentation, published at docs.skylands.ai. It looks like
// the citadel's web client: the same island mark beside the name, the same
// fonts, and the same colors, which src/styles/theme.css maps onto
// Starlight's.
export default defineConfig({
	markdown: {
    	remarkPlugins: [remarkReplaceVersion],
  	},
	vite: {
    plugins: [
      {
        name: 'watch-package-json',
        configureServer(server) {
          server.watcher.add(pkgPath);
          server.watcher.on('change', (file) => {
            if (file === pkgPath) {
              server.restart();
            }
          });
        },
      },
    ],
  },
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
			//editLink: { baseUrl: 'https://github.com/skylands-ai/skylands/edit/main/docsite/' },
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
					items: [
						{ label: 'Installation', slug: 'getting-started/installation' },
						{ label: 'Start Devworld', slug: 'getting-started/devworld' },
						{ label: 'Create a World', slug: 'getting-started/create-world' },
						{ label: 'Create the first Approver', slug: 'getting-started/create-approver' },						
						{ label: 'Register a Repo', slug: 'getting-started/register-repo' },
						{ label: 'Chat With an Agent', slug: 'getting-started/agent-chat' },
					],
				},
				{
					label: 'Concepts',
					items: [
						{label: 'Worlds', slug: 'concepts/worlds'},
						{label: 'Citadel', slug: 'concepts/citadel'},
						{label: 'Islands', slug: 'concepts/islands'},
						{label: 'Terraces', slug: 'concepts/terraces'},
						{label: 'Approvers', slug: 'concepts/approvers'},
						{label: 'Airships', slug: 'concepts/airships'},
						{label: 'Themes', slug: 'concepts/themes'},
						{label: 'Inbox', slug: 'concepts/inbox'},
					],
				},
				{
					label: 'Operations',
					items: [],
				}
			],
		}),
	],
});
