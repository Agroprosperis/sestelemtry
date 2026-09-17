// Stroke paths for the economics KPI icons (see KpiIcon in kpiIcons.tsx).
// Kept in a component-free module so React Fast Refresh stays happy.

export const KPI_ICONS = {
  chart: 'M3 21h18M7 21V9m5 12V3m5 18v-8',
  zap: 'M13 2 3 14h7l-1 8 11-13h-7z',
  database:
    'M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3zm0 0v12c0 1.7 3.6 3 8 3s8-1.3 8-3V6m-16 6c0 1.7 3.6 3 8 3s8-1.3 8-3',
  briefcase:
    'M9 6V5a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2v1M5 6h14a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2zM3 12h18',
  trendUp: 'm3 17 6-6 4 4 8-8m0 0h-5m5 0v5',
  sun: 'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8zM12 2v2m0 16v2M4.9 4.9l1.4 1.4m11.4 11.4 1.4 1.4M2 12h2m16 0h2M4.9 19.1l1.4-1.4m11.4-11.4 1.4-1.4',
  battery: 'M3 9a2 2 0 0 1 2-2h11a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2zm18 2v2M7 10v4m4-4v4',
  percent: 'M19 5 5 19M7.5 5a1.8 1.8 0 1 0 0 3.6 1.8 1.8 0 0 0 0-3.6zm9 10.4a1.8 1.8 0 1 0 0 3.6 1.8 1.8 0 0 0 0-3.6z',
  target:
    'M12 12m-9 0a9 9 0 1 0 18 0a9 9 0 1 0-18 0M12 12m-5 0a5 5 0 1 0 10 0a5 5 0 1 0-10 0M12 12m-1 0a1 1 0 1 0 2 0a1 1 0 1 0-2 0',
  home: 'M3 10.5 12 3l9 7.5M5 9.5V21h14V9.5',
  tower: 'M12 3 7 21M12 3l5 18M9.1 10h5.8M7.6 15.5h8.8M6 21h12',
  exportArrow: 'M12 13V3m0 0L8 7m4-4 4 4M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2',
  clock: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zm0 4v5l3.5 2',
  cycle: 'M21 12a9 9 0 1 1-3-6.7M21 3v6h-6',
} as const
