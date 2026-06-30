import { useCallback, useEffect, useRef, useState } from "react";
import { api, type AnalyticsFilters, type AnalyticsReport } from "../lib/api";

export function useAnalyticsReport(filters: AnalyticsFilters) {
  const [report, setReport] = useState<AnalyticsReport | null>(null);
  const [error, setError] = useState("");
  const [refreshing, setRefreshing] = useState(false);
  const [revision, setRevision] = useState(0);
  const requestID = useRef(0);
  const forceNext = useRef(false);

  const reload = useCallback(() => {
    forceNext.current = true;
    setRevision((value) => value + 1);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    const id = ++requestID.current;
    const force = forceNext.current;
    forceNext.current = false;
    setRefreshing(true);
    setError("");

    api.analyticsReport(filters, controller.signal, force)
      .then((value) => {
        if (id === requestID.current) setReport(value);
      })
      .catch((reason: unknown) => {
        if (!controller.signal.aborted && id === requestID.current) {
          setError(reason instanceof Error ? reason.message : "Could not load analytics");
        }
      })
      .finally(() => {
        if (id === requestID.current) setRefreshing(false);
      });

    return () => controller.abort();
  }, [filters, revision]);

  return { report, error, refreshing, loading: refreshing && report === null, reload };
}
