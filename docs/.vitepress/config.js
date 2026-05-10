import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'cotel',
  description: 'Claude Code Telemetry — self-hosted OTLP ingest + analytics dashboard',
  base: '/cotel/',
  ignoreDeadLinks: [/^http:\/\/localhost/],

  themeConfig: {
    nav: [
      { text: 'Home', link: '/' },
      { text: 'Operations', link: '/operations/cloudflare-tunnel-remote' },
      { text: 'Decisions', link: '/decisions/' },
      { text: 'Design', link: '/design/' },
      {
        text: 'GitHub',
        link: 'https://github.com/Flopsstuff/cotel',
      },
    ],

    sidebar: {
      '/operations/': [
        {
          text: 'Operations',
          items: [
            { text: 'Users and Authentication', link: '/operations/users-and-auth' },
            { text: 'Cloudflare Tunnel — Token Mode', link: '/operations/cloudflare-tunnel-remote' },
            { text: 'Cloudflare Tunnel — Local Config', link: '/operations/cloudflare-tunnel-local' },
            { text: 'Export / Import', link: '/operations/export-import' },
          ],
        },
      ],
      '/decisions/': [
        {
          text: 'Architecture Decisions',
          items: [
            { text: 'ADR-0001 — Storage Engine', link: '/decisions/0001-storage' },
            { text: 'ADR-0002 — Dashboard React SPA', link: '/decisions/0002-dashboard-react-spa' },
            { text: 'ADR-0003 — Release Policy', link: '/decisions/0003-release-policy' },
            { text: 'ADR-0004 — Multi-User Separation', link: '/decisions/0004-multi-user-separation' },
            { text: 'ADR-0005 — Export/Import Format', link: '/decisions/0005-export-import-format' },
            { text: 'ADR-0006 — Cloudflare Tunnel + Token Auth', link: '/decisions/0006-cloudflare-tunnel-and-token-auth' },
          ],
        },
      ],
      '/design/': [
        {
          text: 'Design Docs',
          items: [
            { text: 'Information Architecture', link: '/design/information-architecture' },
            { text: 'Pages', link: '/design/pages' },
            { text: 'Components', link: '/design/components' },
            { text: 'Design Tokens', link: '/design/tokens' },
            { text: 'Wireframes', link: '/design/wireframes' },
            { text: 'FLO-8 Wireframes', link: '/design/FLO-8-wireframes' },
          ],
        },
      ],
    },

    socialLinks: [
      { icon: 'github', link: 'https://github.com/Flopsstuff/cotel' },
    ],

    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Flopsstuff',
    },

    editLink: {
      pattern: 'https://github.com/Flopsstuff/cotel/edit/main/docs/:path',
      text: 'Edit this page on GitHub',
    },
  },
})
