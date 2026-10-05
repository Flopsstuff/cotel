import { defineConfig } from 'vitepress'

export default defineConfig({
  title: 'cotel',
  description: 'Claude Code Telemetry — self-hosted OTLP ingest + analytics dashboard',
  base: '/cotel/',
  // /FLO/... are Paperclip board tracker references, not VitePress pages — they
  // resolve on the board, not on GitHub Pages, so exclude them from the dead-link check.
  ignoreDeadLinks: [/^http:\/\/localhost/, /^\/FLO\//],

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
            { text: 'JSON API Reference', link: '/operations/api-reference' },
            { text: 'Cloudflare Tunnel — Token Mode', link: '/operations/cloudflare-tunnel-remote' },
            { text: 'Cloudflare Tunnel — Local Config', link: '/operations/cloudflare-tunnel-local' },
            { text: 'Production /healthz probe', link: '/operations/health-probe' },
            { text: 'Export / Import', link: '/operations/export-import' },
            { text: 'DuckDB Recovery', link: '/operations/duckdb-recovery' },
            { text: 'README Screenshots', link: '/operations/screenshots' },
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
            { text: 'ADR-0007 — GitHub Intake Security', link: '/decisions/0007-github-intake-security' },
            { text: 'ADR-0008 — Per-Agent Telemetry Identity', link: '/decisions/0008-per-agent-telemetry-identity' },
            { text: 'ADR-0009 — Daily Usage Unknown Sentinel', link: '/decisions/0009-daily-usage-unknown-sentinel' },
            { text: 'ADR-0010 — Schema Version Guard', link: '/decisions/0010-schema-version-guard' },
            { text: 'ADR-0011 — Users List Ranged Stats', link: '/decisions/0011-users-list-ranged-stats-and-server-side-sort' },
            { text: 'ADR-0012 — Tools List Ranged Stats', link: '/decisions/0012-tools-list-ranged-stats-and-server-side-sort' },
            { text: 'ADR-0013 — Spans Has No Derived Columns', link: '/decisions/0013-spans-has-no-derived-columns' },
            { text: 'ADR-0014 — Overview Single Range Selector', link: '/decisions/0014-overview-single-range-selector' },
            { text: 'ADR-0015 — Overview Activity and Cost One Block', link: '/decisions/0015-overview-activity-and-cost-one-block' },
            { text: 'ADR-0016 — Overview Activity Grid', link: '/decisions/0016-overview-activity-grid' },
            { text: 'ADR-0017 — Chart Palette Ruler Is Pinned', link: '/decisions/0017-chart-palette-ruler-is-pinned' },
            { text: 'ADR-0018 — DuckDB Go v2 Driver', link: '/decisions/0018-duckdb-go-v2-driver' },
            { text: 'ADR-0019 — CI Never Mutates an Issue (superseded)', link: '/decisions/0019-ci-never-mutates-an-issue' },
            { text: 'ADR-0020 — Recovery Arrives as a New Issue (superseded)', link: '/decisions/0020-recovery-arrives-as-a-new-issue' },
            { text: "ADR-0021 — Recovery Wakes the Alert's Assignee", link: '/decisions/0021-recovery-wakes-the-alerts-assignee' },
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
