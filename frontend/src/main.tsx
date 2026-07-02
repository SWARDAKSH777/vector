import React, { Suspense, lazy, useEffect, useState } from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter, Navigate, Route, Routes, useNavigate } from "react-router-dom";
import { AuthProvider, useAuth } from "./lib/auth";
import { api } from "./lib/api";
import "./styles.css";

const loadSetup = () => import("./pages/Setup");
const loadLogin = () => import("./pages/Login");
const loadDashboard = () => import("./pages/Dashboard");
const loadLinks = () => import("./pages/Links");
const loadAnalytics = () => import("./pages/Analytics");
const loadQR = () => import("./pages/QR");
const loadDomains = () => import("./pages/Domains");
const loadSettings = () => import("./pages/Settings");
const loadAdmin = () => import("./pages/Admin");
const SetupPage = lazy(() => loadSetup().then((module) => ({ default: module.SetupPage })));
const LoginPage = lazy(() => loadLogin().then((module) => ({ default: module.LoginPage })));
const DashboardPage = lazy(() => loadDashboard().then((module) => ({ default: module.DashboardPage })));
const LinksPage = lazy(() => loadLinks().then((module) => ({ default: module.LinksPage })));
const AnalyticsPage = lazy(() => loadAnalytics().then((module) => ({ default: module.AnalyticsPage })));
const QRPage = lazy(() => loadQR().then((module) => ({ default: module.QRPage })));
const DomainsPage = lazy(() => loadDomains().then((module) => ({ default: module.DomainsPage })));
const SettingsPage = lazy(() => loadSettings().then((module) => ({ default: module.SettingsPage })));
const AdminPage = lazy(() => loadAdmin().then((module) => ({ default: module.AdminPage })));

function LoadingScreen() { return <div className="flex items-center justify-center h-screen"><div className="animate-spin h-5 w-5 rounded-full border-2 border-primary border-t-transparent"/></div>; }
function SetupGuard({children}:{children:React.ReactNode}) {
  const [checked,setChecked]=useState(false);
  const [error,setError]=useState("");
  const navigate=useNavigate();
  useEffect(()=>{
    api.setupStatus()
      .then((status)=>{if(!status.setup_complete)navigate("/setup",{replace:true});})
      .catch((reason: unknown)=>setError(reason instanceof Error ? reason.message : "Could not check setup status"))
      .finally(()=>setChecked(true));
  },[navigate]);
  if (!checked) return <LoadingScreen/>;
  if (error) return <div className="min-h-screen flex items-center justify-center p-6"><div className="max-w-md rounded-xl border border-destructive/30 bg-card p-5 text-center"><p className="font-semibold">Vector is unavailable</p><p className="mt-2 text-sm text-muted-foreground">{error}</p><button className="mt-4 rounded-lg bg-primary px-4 py-2 text-sm text-primary-foreground" onClick={()=>window.location.reload()}>Retry</button></div></div>;
  return <>{children}</>;
}
function ProtectedRoute({children}:{children:React.ReactNode}) {
  const {user,loading,error,retry}=useAuth();
  if(loading)return <LoadingScreen/>;
  if(error)return <div className="min-h-screen flex items-center justify-center p-6"><div className="max-w-md rounded-xl border border-destructive/30 bg-card p-5 text-center"><p className="font-semibold">Session check failed</p><p className="mt-2 text-sm text-muted-foreground">{error}</p><button className="mt-4 rounded-lg bg-primary px-4 py-2 text-sm text-primary-foreground" onClick={()=>void retry()}>Retry</button></div></div>;
  if(!user)return <Navigate to="/login" replace/>;
  return <>{children}</>;
}

function AdminRoute({children}:{children:React.ReactNode}) {
  const {user,loading,error,retry}=useAuth();
  if(loading)return <LoadingScreen/>;
  if(error)return <div className="min-h-screen flex items-center justify-center p-6"><div className="max-w-md rounded-xl border border-destructive/30 bg-card p-5 text-center"><p className="font-semibold">Session check failed</p><p className="mt-2 text-sm text-muted-foreground">{error}</p><button className="mt-4 rounded-lg bg-primary px-4 py-2 text-sm text-primary-foreground" onClick={()=>void retry()}>Retry</button></div></div>;
  if(!user)return <Navigate to="/login" replace/>;
  if(user.role!=="admin" || !user.multi_user)return <Navigate to="/" replace/>;
  return <>{children}</>;
}

function AuthenticatedPreload() {
  const { user } = useAuth();
  useEffect(() => {
    if (!user) return;
    let cancelled = false;
    const run = () => {
      if (cancelled) return;
      void Promise.allSettled([loadDashboard(), loadLinks(), loadAnalytics(), loadQR(), loadDomains(), loadSettings(), ...(user.role === "admin" && user.multi_user ? [loadAdmin()] : [])]);
      void Promise.allSettled([api.analyticsReport({ range: "30d" }), api.listLinks(), api.listDomains(), api.getPrivacySettings(), ...(user.role === "admin" ? [api.getIPInfoTokenStatus()] : [])]);
    };
    const windowWithIdle = window as Window & { requestIdleCallback?: (callback: () => void, options?: { timeout: number }) => number; cancelIdleCallback?: (id: number) => void };
    if (windowWithIdle.requestIdleCallback) {
      const id = windowWithIdle.requestIdleCallback(run, { timeout: 1200 });
      return () => { cancelled = true; windowWithIdle.cancelIdleCallback?.(id); };
    }
    const timer = window.setTimeout(run, 250);
    return () => { cancelled = true; window.clearTimeout(timer); };
  }, [user]);
  return null;
}

function App() { return <BrowserRouter><AuthProvider><AuthenticatedPreload/><SetupGuard><Suspense fallback={<LoadingScreen/>}><Routes><Route path="/setup" element={<SetupPage/>}/><Route path="/login" element={<LoginPage/>}/><Route path="/" element={<ProtectedRoute><DashboardPage/></ProtectedRoute>}/><Route path="/links" element={<ProtectedRoute><LinksPage/></ProtectedRoute>}/><Route path="/analytics" element={<ProtectedRoute><AnalyticsPage/></ProtectedRoute>}/><Route path="/qr" element={<ProtectedRoute><QRPage/></ProtectedRoute>}/><Route path="/domains" element={<ProtectedRoute><DomainsPage/></ProtectedRoute>}/><Route path="/settings" element={<ProtectedRoute><SettingsPage/></ProtectedRoute>}/><Route path="/admin" element={<AdminRoute><AdminPage/></AdminRoute>}/><Route path="*" element={<Navigate to="/" replace/>}/></Routes></Suspense></SetupGuard></AuthProvider></BrowserRouter>; }

ReactDOM.createRoot(document.getElementById("root")!).render(<React.StrictMode><App/></React.StrictMode>);
