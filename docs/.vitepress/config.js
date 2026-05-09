import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'cotel',
  description: 'Claude Code Telemetry — self-hosted OTLP ingest + analytics dashboard',
  base: '/cotel/',
  ignoreDeadLinks: [/^http:\/\/localhost/],

  themeConfig: {
    nav: [
      { text: 'Home', link: '/' },
      { text: 'Decisions', link: '/decisions/' },
      { text: 'Design', link: '/design/' },
      {
        text: 'GitHub',
        link: 'https://github.com/Flopsstuff/cotel',
      },
    ],

    sidebar: {
      '/decisions/': [
        {
          text: 'Architecture Decisions',
          items: [
            { text: 'ADR-0001 — Storage Engine', link: '/decisions/0001-storage' },
            { text: 'ADR-0003 — Release Policy', link: '/decisions/0003-release-policy' },
            { text: 'ADR-0004 — Multi-User Separation', link: '/decisions/0004-multi-user-separation' },
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
