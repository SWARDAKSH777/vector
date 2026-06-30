import React, { useMemo, useRef, useState } from "react";
import { geoNaturalEarth1, geoPath } from "d3-geo";
import { feature } from "topojson-client";
import worldData from "world-atlas/countries-110m.json";
import countries from "i18n-iso-countries";
import en from "i18n-iso-countries/langs/en.json";
import type { GeoCountryStat } from "../lib/api";

countries.registerLocale(en);

type Geography = {
  id?: string | number;
  properties?: { name?: string };
  type: string;
  coordinates?: unknown;
};

type TooltipState = {
  code: string;
  name: string;
  clicks: number;
  unique: number;
  share: number;
  x: number;
  y: number;
};

interface WorldAnalyticsMapProps {
  data: GeoCountryStat[];
  selectedCountry?: string;
  onSelectCountry?: (code: string) => void;
}

export function countryName(code: string): string {
  return countries.getName(code, "en") || code || "Unknown";
}

export function WorldAnalyticsMap({ data, selectedCountry, onSelectCountry }: WorldAnalyticsMapProps) {
  const [tooltip, setTooltip] = useState<TooltipState | null>(null);
  const svgRef = useRef<SVGSVGElement>(null);
  const WIDTH = 960;
  const HEIGHT = 460;

  const geographies = useMemo(() => {
    const col = feature(worldData as any, (worldData as any).objects.countries) as any;
    return col.features as Geography[];
  }, []);

  const { path } = useMemo(() => {
    const proj = geoNaturalEarth1().fitExtent(
      [[10, 10], [WIDTH - 10, HEIGHT - 10]],
      { type: "Sphere" } as any
    );
    return { projection: proj, path: geoPath(proj) };
  }, []);

  const values = useMemo(
    () => new Map(data.map((d) => [d.code.toUpperCase(), d])),
    [data]
  );
  const total = data.reduce((s, d) => s + d.clicks, 0);
  const maximum = Math.max(1, ...data.map((d) => d.clicks));
  const selected = selectedCountry?.toUpperCase() ?? "";

  // Log scale intensity for fill opacity
  function intensity(clicks: number) {
    return 0.18 + 0.82 * (Math.log1p(clicks) / Math.log1p(maximum));
  }

  function handleSelect(code: string) {
    onSelectCountry?.(selected === code ? "" : code);
  }

  function openTooltip(e: React.PointerEvent, code: string, item: GeoCountryStat) {
    const box = svgRef.current?.getBoundingClientRect();
    if (!box) return;
    setTooltip({
      code,
      name: countryName(code),
      clicks: item.clicks,
      unique: item.unique,
      share: total > 0 ? Math.round((item.clicks / total) * 1000) / 10 : 0,
      x: e.clientX - box.left,
      y: e.clientY - box.top,
    });
  }

  function moveTooltip(e: React.PointerEvent, code: string, item: GeoCountryStat) {
    const box = svgRef.current?.getBoundingClientRect();
    if (!box || !tooltip) return;
    setTooltip((prev) =>
      prev ? { ...prev, x: e.clientX - box.left, y: e.clientY - box.top } : prev
    );
  }

  const top5 = useMemo(
    () => [...data].sort((a, b) => b.clicks - a.clicks).slice(0, 5),
    [data]
  );

  return (
    <div className="flex flex-col gap-0 overflow-hidden rounded-xl border border-border bg-card">
      {/* ── Map ── */}
      <div className="relative bg-muted/10" style={{ minHeight: 200 }}>
        <svg
          ref={svgRef}
          viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
          className="block h-auto w-full select-none"
          role="img"
          aria-label="World map showing click locations by country"
          onPointerLeave={() => setTooltip(null)}
        >
          {/* Ocean / globe background */}
          <path
            d={path({ type: "Sphere" } as any) ?? ""}
            fill="var(--color-muted)"
            fillOpacity={0.25}
            stroke="var(--color-border)"
            strokeWidth={0.5}
          />

          {geographies.map((geo) => {
            const numeric = String(geo.id ?? "").padStart(3, "0");
            const code = countries.numericToAlpha2(numeric) ?? "";
            const item = values.get(code);
            const isSelected = !!code && selected === code;
            const d = path(geo as any) ?? "";
            if (!d) return null;

            const center = path.centroid(geo as any);
            const area = path.area(geo as any);
            // Show a dot for very small countries
            const tinyCountry = Boolean(
              item &&
                Number.isFinite(center[0]) &&
                Number.isFinite(center[1]) &&
                area < 12
            );

            return (
              <g key={numeric}>
                <path
                  d={d}
                  fill={
                    isSelected
                      ? "var(--color-primary)"
                      : item
                      ? "var(--color-primary)"
                      : "var(--color-muted)"
                  }
                  fillOpacity={
                    isSelected
                      ? Math.max(intensity(item!.clicks), 0.55) + 0.12
                      : item
                      ? intensity(item.clicks)
                      : 0.32
                  }
                  stroke={
                    isSelected
                      ? "var(--color-foreground)"
                      : "var(--color-background)"
                  }
                  strokeWidth={isSelected ? 2 : 0.6}
                  vectorEffect="non-scaling-stroke"
                  className={
                    item
                      ? "cursor-pointer outline-none transition-[fill-opacity] duration-100 hover:fill-opacity-90"
                      : "outline-none"
                  }
                  tabIndex={item ? 0 : -1}
                  role={item ? "button" : undefined}
                  aria-label={
                    item
                      ? `${countryName(code)}: ${item.clicks.toLocaleString()} clicks`
                      : undefined
                  }
                  aria-pressed={item ? isSelected : undefined}
                  onPointerEnter={(e) => item && openTooltip(e, code, item)}
                  onPointerMove={(e) => item && moveTooltip(e, code, item)}
                  onPointerLeave={() => setTooltip(null)}
                  onFocus={() =>
                    item &&
                    setTooltip({
                      code,
                      name: countryName(code),
                      clicks: item.clicks,
                      unique: item.unique,
                      share: total > 0 ? Math.round((item.clicks / total) * 1000) / 10 : 0,
                      x: WIDTH / 2,
                      y: 36,
                    })
                  }
                  onBlur={() => setTooltip(null)}
                  onClick={() => item && handleSelect(code)}
                  onKeyDown={(e) => {
                    if (!item || (e.key !== "Enter" && e.key !== " ")) return;
                    e.preventDefault();
                    handleSelect(code);
                  }}
                >
                  <title>
                    {item
                      ? `${countryName(code)}: ${item.clicks.toLocaleString()} clicks, ${item.unique.toLocaleString()} unique`
                      : countryName(code)}
                  </title>
                </path>

                {/* Dot marker for tiny countries */}
                {tinyCountry && (
                  <circle
                    cx={center[0]}
                    cy={center[1]}
                    r={isSelected ? 5 : 3.5}
                    fill={isSelected ? "var(--color-foreground)" : "var(--color-primary)"}
                    stroke="var(--color-background)"
                    strokeWidth={1.5}
                    vectorEffect="non-scaling-stroke"
                    pointerEvents="none"
                  />
                )}
              </g>
            );
          })}
        </svg>

        {/* ── Tooltip ── */}
        {tooltip && (
          <div
            className="pointer-events-none absolute z-20 -translate-x-1/2 rounded-xl border border-border bg-card shadow-xl"
            style={{
              left: tooltip.x,
              top: Math.max(8, tooltip.y - 8),
              transform: "translate(-50%, -110%)",
              minWidth: 170,
            }}
          >
            <div className="px-3 pt-2.5 pb-2">
              <p className="text-[13px] font-semibold leading-tight">{tooltip.name}</p>
              <p className="text-[11px] text-muted-foreground mt-0.5">{tooltip.share}% of located traffic</p>
            </div>
            <div className="border-t border-border grid grid-cols-2 divide-x divide-border">
              <div className="px-3 py-2">
                <p className="text-[10px] text-muted-foreground uppercase tracking-wide">Clicks</p>
                <p className="text-sm font-bold tabular-nums mt-0.5">{tooltip.clicks.toLocaleString()}</p>
              </div>
              <div className="px-3 py-2">
                <p className="text-[10px] text-muted-foreground uppercase tracking-wide">Unique</p>
                <p className="text-sm font-bold tabular-nums mt-0.5">{tooltip.unique.toLocaleString()}</p>
              </div>
            </div>
          </div>
        )}

        {/* ── Selected country badge ── */}
        {selected && (
          <div className="absolute top-2 left-2 flex items-center gap-1.5 rounded-full border border-primary/40 bg-primary/10 px-2.5 py-1 text-xs font-medium text-primary">
            <span className="h-1.5 w-1.5 rounded-full bg-primary" />
            Filtered: {countryName(selected)}
          </div>
        )}
      </div>

      {/* ── Footer: legend + top-5 pills ── */}
      <div className="border-t border-border bg-muted/20 px-3 py-2.5 flex flex-col gap-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <p className="text-[11px] text-muted-foreground">
            Click or tap a highlighted country to filter every chart below.
          </p>
          <div className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
            <span>Low</span>
            <div className="flex gap-0.5">
              {[0.18, 0.35, 0.55, 0.75, 1].map((o) => (
                <span
                  key={o}
                  className="h-2.5 w-3.5 rounded-sm bg-primary inline-block"
                  style={{ opacity: o }}
                />
              ))}
            </div>
            <span>High</span>
          </div>
        </div>

        {/* Top-5 pills — visible on all sizes */}
        {top5.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {top5.map((item) => {
              const code = item.code.toUpperCase();
              const isActive = selected === code;
              return (
                <button
                  key={code}
                  onClick={() => handleSelect(code)}
                  className={`flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[11px] font-medium transition-colors ${
                    isActive
                      ? "border-primary bg-primary/10 text-primary"
                      : "border-border bg-card hover:border-primary/50 hover:bg-primary/5"
                  }`}
                >
                  <span
                    className="h-2 w-2 rounded-full bg-primary inline-block shrink-0"
                    style={{ opacity: intensity(item.clicks) }}
                  />
                  {countryName(code)}
                  <span className="text-muted-foreground font-normal">
                    {item.clicks.toLocaleString()}
                  </span>
                </button>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
