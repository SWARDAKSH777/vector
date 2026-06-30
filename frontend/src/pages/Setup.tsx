import React, { useState, useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../lib/api";
import { Button, Input } from "../components/ui";
import { Check, X, Loader2, ShieldCheck, Eye, EyeOff } from "lucide-react";
import { cn } from "../lib/utils";

type Step = "welcome" | "account" | "domain" | "nginx" | "done";

export function SetupPage() {
  const navigate = useNavigate();
  const [step, setStep] = useState<Step>("welcome");

  // installer-generated one-time bootstrap gate
  const [bootstrapLoading, setBootstrapLoading] = useState(true);
  const [bootstrapRequired, setBootstrapRequired] = useState(true);
  const [bootstrapAuthenticated, setBootstrapAuthenticated] = useState(false);
  const [bootstrapAvailable, setBootstrapAvailable] = useState(true);
  const [bootstrapMessage, setBootstrapMessage] = useState("");
  const [bootstrapUsername, setBootstrapUsername] = useState("");
  const [bootstrapPassword, setBootstrapPassword] = useState("");
  const [bootstrapError, setBootstrapError] = useState("");
  const [bootstrapSubmitting, setBootstrapSubmitting] = useState(false);
  const [showBootstrapPassword, setShowBootstrapPassword] = useState(false);

  // account
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [accountError, setAccountError] = useState("");

  // domain
  const [domain, setDomain] = useState("");
  const [domainCheck, setDomainCheck] = useState<"idle" | "checking" | "ok" | "error">("idle");
  const [domainError, setDomainError] = useState("");
  const [cfToken, setCfToken] = useState("");
  const [showToken, setShowToken] = useState(false);
  const domainTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  // nginx
  const [nginxLog, setNginxLog] = useState("");
  const [nginxLoading, setNginxLoading] = useState(false);
  const [nginxDone, setNginxDone] = useState(false);
  const [finalDomain, setFinalDomain] = useState("");

  // submit
  const [submitLoading, setSubmitLoading] = useState(false);
  const [submitError, setSubmitError] = useState("");

  useEffect(() => {
    api.setupStatus()
      .then((status) => {
        if (status.setup_complete) {
          navigate("/login", { replace: true });
          return;
        }
        setBootstrapRequired(status.bootstrap_required);
        setBootstrapAuthenticated(status.bootstrap_authenticated);
        setBootstrapAvailable(status.bootstrap_available);
        setBootstrapMessage(status.bootstrap_message || "");
      })
      .catch((err: any) => {
        setBootstrapAvailable(false);
        setBootstrapMessage(err?.message || "Could not load bootstrap status");
      })
      .finally(() => setBootstrapLoading(false));
  }, [navigate]);

  async function handleBootstrapLogin(e: React.FormEvent) {
    e.preventDefault();
    setBootstrapError("");
    setBootstrapSubmitting(true);
    try {
      await api.bootstrapLogin(bootstrapUsername.trim(), bootstrapPassword);
      const status = await api.setupStatus();
      if (!status.bootstrap_authenticated) throw new Error("Bootstrap session was not accepted");
      setBootstrapAuthenticated(true);
      setBootstrapPassword("");
    } catch (err: any) {
      setBootstrapError(err?.message || "Bootstrap login failed");
    } finally {
      setBootstrapSubmitting(false);
    }
  }

  // Auto-check DNS as user types. Abort previous in-flight checks so an old
  // response cannot overwrite the status for the value currently on screen.
  useEffect(() => {
    if (bootstrapRequired && !bootstrapAuthenticated) return;
    if (!domain.trim()) { setDomainCheck("idle"); setDomainError(""); return; }
    clearTimeout(domainTimer.current);
    const controller = new AbortController();
    setDomainCheck("checking");
    domainTimer.current = setTimeout(async () => {
      try {
        const r = await api.setupCheckDomain(domain.trim(), cfToken.trim() || undefined, controller.signal);
        if (r.ok) { setDomainCheck("ok"); setDomainError(""); }
        else { setDomainCheck("error"); setDomainError(r.error || "Domain does not reach this server"); }
      } catch (err: any) {
        if (err?.name === "AbortError") return;
        setDomainCheck("error");
        setDomainError(err?.message || "Could not contact the setup API. Check the Vector and Nginx service logs.");
      }
    }, 1000);
    return () => { clearTimeout(domainTimer.current); controller.abort(); };
  }, [domain, cfToken, bootstrapRequired, bootstrapAuthenticated]);

  function handleAccountNext(e: React.FormEvent) {
    e.preventDefault();
    setAccountError("");
    if (password !== confirmPassword) { setAccountError("Passwords don't match"); return; }
    if ([...password].length < 15) { setAccountError("Use a passphrase of at least 15 characters"); return; }
    setStep("domain");
  }

  async function handleDomainNext() {
    setSubmitError(""); setSubmitLoading(true);
    try {
      const res = await api.setupSubmit({
        domain: domain.trim(),
        admin_email: email,
        admin_password: password,
        cloudflare_token: cfToken.trim() || undefined,
      });
      setFinalDomain(res.domain);
      setStep(res.domain ? "nginx" : "done");
    } catch (err: any) { setSubmitError(err.message); }
    finally { setSubmitLoading(false); }
  }

  async function handleNginx() {
    setNginxLoading(true); setNginxLog("");
    try {
      const data = await api.setupNginx(finalDomain);
      setNginxLog(data.log || data.error || "");
      setNginxDone(Boolean(data.ok));
    } catch (err: any) { setNginxLog("Error: " + err.message); }
    finally { setNginxLoading(false); }
  }

  if (bootstrapLoading) {
    return (
      <div className="min-h-[100dvh] bg-background flex items-center justify-center">
        <Loader2 className="w-6 h-6 animate-spin text-primary" />
      </div>
    );
  }

  if (bootstrapRequired && !bootstrapAuthenticated) {
    return (
      <div className="min-h-[100dvh] bg-background flex items-center justify-center p-4">
        <div className="w-full max-w-md">
          <div className="flex items-center gap-2.5 justify-center mb-8">
            <div className="w-10 h-10 rounded-xl bg-primary flex items-center justify-center">
              <ShieldCheck className="w-5 h-5 text-primary-foreground" />
            </div>
            <span className="font-bold text-2xl tracking-tight">VECTOR</span>
          </div>

          <div className="bg-card border border-border rounded-2xl shadow-sm overflow-hidden">
            <div className="px-6 py-5 border-b border-border">
              <h1 className="font-semibold text-lg">Secure setup login</h1>
              <p className="text-xs text-muted-foreground mt-1">
                Enter the one-time credentials printed by <code>install.sh</code>.
              </p>
            </div>

            <form onSubmit={handleBootstrapLogin} className="p-6 space-y-4">
              <div className="p-3 rounded-lg bg-primary/5 border border-primary/15 text-xs text-muted-foreground leading-relaxed">
                This gate prevents anyone who discovers the server IP from claiming the initial administrator account.
              </div>

              <Input
                label="Bootstrap username"
                placeholder="vector-bootstrap-xxxxxxxx"
                value={bootstrapUsername}
                onChange={(e) => setBootstrapUsername(e.target.value)}
                autoComplete="username"
                required
                autoFocus
                disabled={!bootstrapAvailable}
              />

              <div className="flex flex-col gap-1.5">
                <label className="text-xs font-medium text-foreground">Bootstrap password</label>
                <div className="relative flex items-center">
                  <input
                    type={showBootstrapPassword ? "text" : "password"}
                    placeholder="Installer-generated password"
                    value={bootstrapPassword}
                    onChange={(e) => setBootstrapPassword(e.target.value)}
                    autoComplete="current-password"
                    required
                    disabled={!bootstrapAvailable}
                    className="flex h-9 w-full rounded-lg border border-input bg-background px-3 pr-10 py-2 text-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring font-mono disabled:cursor-not-allowed disabled:opacity-50"
                  />
                  <button
                    type="button"
                    onClick={() => setShowBootstrapPassword((v) => !v)}
                    className="absolute right-3 text-muted-foreground hover:text-foreground transition-colors"
                    tabIndex={-1}
                  >
                    {showBootstrapPassword ? <EyeOff className="w-4 h-4" /> : <Eye className="w-4 h-4" />}
                  </button>
                </div>
              </div>

              {!bootstrapAvailable && (
                <div className="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg space-y-1">
                  <p>{bootstrapMessage || "Bootstrap credentials are unavailable."}</p>
                  <p className="text-xs font-mono">sudo vector-bootstrap-reset</p>
                </div>
              )}
              {bootstrapError && <p className="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{bootstrapError}</p>}

              <Button className="w-full" size="lg" type="submit" loading={bootstrapSubmitting} disabled={!bootstrapAvailable}>
                Unlock Setup →
              </Button>
            </form>
          </div>
          <p className="text-center text-xs text-muted-foreground mt-5">Vector · Protected bootstrap setup</p>
        </div>
      </div>
    );
  }

  const visibleSteps: Step[] = ["welcome", "account", "domain", ...(finalDomain ? ["nginx" as Step] : []), "done"];
  const stepIdx = visibleSteps.indexOf(step);

  const domainSuffix = () => {
    if (!domain) return null;
    if (domainCheck === "checking") return <Loader2 className="w-4 h-4 animate-spin text-muted-foreground" />;
    if (domainCheck === "ok") return <Check className="w-4 h-4 text-success" />;
    if (domainCheck === "error") return <X className="w-4 h-4 text-destructive" />;
    return null;
  };

  return (
    <div className="min-h-[100dvh] bg-background flex items-center justify-center p-4">
      <div className="w-full max-w-md">
        {/* Logo */}
        <div className="flex items-center gap-2.5 justify-center mb-8">
          <div className="w-10 h-10 rounded-xl bg-primary flex items-center justify-center">
            <svg className="w-5 h-5 text-primary-foreground" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
              <path d="M13 5l7 7-7 7M5 12h15" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
          </div>
          <span className="font-bold text-2xl tracking-tight">VECTOR</span>
        </div>

        {/* Step dots */}
        <div className="flex items-center gap-2 mb-6 justify-center">
          {visibleSteps.map((s, i) => (
            <React.Fragment key={s}>
              <div className={cn("w-2 h-2 rounded-full transition-colors",
                stepIdx === i ? "bg-primary" : stepIdx > i ? "bg-primary/40" : "bg-muted")} />
              {i < visibleSteps.length - 1 && <div className="w-6 h-px bg-border" />}
            </React.Fragment>
          ))}
        </div>

        <div className="bg-card border border-border rounded-2xl shadow-sm overflow-hidden">

          {/* WELCOME */}
          {step === "welcome" && (
            <div className="p-7 text-center">
              <div className="w-16 h-16 rounded-2xl bg-primary/10 flex items-center justify-center mx-auto mb-5">
                <ShieldCheck className="w-8 h-8 text-primary" />
              </div>
              <h1 className="text-xl font-bold mb-2">Welcome to Vector</h1>
              <p className="text-muted-foreground text-sm mb-6 leading-relaxed">
                Self-hosted URL shortener. Setup takes 2 minutes.
              </p>
              <div className="text-left space-y-3 mb-6">
                {[
                  "Create your admin account",
                  "Connect your domain with optional Cloudflare integration",
                  "Auto-configure nginx + SSL certificate",
                ].map((s, i) => (
                  <div key={i} className="flex items-center gap-3">
                    <div className="w-5 h-5 rounded-full bg-primary/10 text-primary flex items-center justify-center text-xs font-bold shrink-0">{i + 1}</div>
                    <span className="text-sm text-muted-foreground">{s}</span>
                  </div>
                ))}
              </div>
              <Button className="w-full" size="lg" onClick={() => setStep("account")}>Get Started →</Button>
            </div>
          )}

          {/* ACCOUNT */}
          {step === "account" && (
            <>
              <div className="px-6 py-4 border-b border-border">
                <h2 className="font-semibold">Admin Account</h2>
                <p className="text-xs text-muted-foreground mt-0.5">You'll use these credentials to log in</p>
              </div>
              <form onSubmit={handleAccountNext} className="p-6 space-y-4">
                <Input label="Email" type="email" placeholder="admin@yourdomain.com"
                  value={email} onChange={(e) => setEmail(e.target.value)} required autoFocus />
                <Input label="Password" type="password" placeholder="At least 15 characters"
                  value={password} onChange={(e) => setPassword(e.target.value)} required />
                <Input label="Confirm password" type="password" placeholder="Repeat password"
                  value={confirmPassword} onChange={(e) => setConfirmPassword(e.target.value)} required />
                {accountError && <p className="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{accountError}</p>}
                <div className="flex gap-3">
                  <Button type="button" variant="secondary" onClick={() => setStep("welcome")}>Back</Button>
                  <Button type="submit" className="flex-1">Next →</Button>
                </div>
              </form>
            </>
          )}

          {/* DOMAIN */}
          {step === "domain" && (
            <>
              <div className="px-6 py-4 border-b border-border">
                <h2 className="font-semibold">Domain & Cloudflare</h2>
                <p className="text-xs text-muted-foreground mt-0.5">Create an A/AAAA/CNAME record for this hostname first</p>
              </div>
              <div className="p-6 space-y-4">

                {/* Domain field */}
                <Input
                  label="Domain *"
                  placeholder="links.yourdomain.com"
                  value={domain}
                  onChange={(e) => setDomain(e.target.value.toLowerCase().trim())}
                  required
                  autoFocus
                  error={domainCheck === "error" ? domainError : undefined}
                  hint={domainCheck === "ok"
                    ? (cfToken ? "✓ Cloudflare token and zone verified; Vector will create/validate DNS during setup" : "✓ Domain DNS and port 80 are reachable")
                    : (cfToken ? "Vector will verify the zone and DNS record through Cloudflare" : "Vector will check DNS and port 80; a default nginx 404 is accepted") }
                  suffix={domainSuffix()}
                />

                {/* CF Token field */}
                <div className="flex flex-col gap-1.5">
                  <label className="text-xs font-medium text-foreground">
                    Cloudflare API Token <span className="text-muted-foreground font-normal">(optional — enables auto DNS + SSL)</span>
                  </label>
                  <div className="relative flex items-center">
                    <input
                      type={showToken ? "text" : "password"}
                      placeholder="Paste your Cloudflare API token"
                      value={cfToken}
                      onChange={(e) => setCfToken(e.target.value)}
                      className="flex h-9 w-full rounded-lg border border-input bg-background px-3 pr-10 py-2 text-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring font-mono"
                    />
                    <button
                      type="button"
                      onClick={() => setShowToken((v) => !v)}
                      className="absolute right-3 text-muted-foreground hover:text-foreground transition-colors"
                    >
                      {showToken ? <EyeOff className="w-3.5 h-3.5" /> : <Eye className="w-3.5 h-3.5" />}
                    </button>
                  </div>
                  {cfToken ? (
                    <p className="text-xs text-success">
                      ✓ Token will be saved encrypted — Vector will use it to manage DNS records and get SSL certificates via DNS-01 challenge (no port 80 needed)
                    </p>
                  ) : (
                    <p className="text-xs text-muted-foreground">
                      Create at{" "}
                      <a href="https://dash.cloudflare.com/profile/api-tokens" target="_blank" rel="noreferrer"
                        className="text-primary underline">dash.cloudflare.com/profile/api-tokens</a>
                      {" "}with <strong>Zone:Read</strong> and <strong>DNS:Edit</strong> permissions.
                      Without this, SSL will use HTTP-01 (requires port 80 to be free).
                    </p>
                  )}
                </div>

                {submitError && (
                  <p className="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{submitError}</p>
                )}

                <div className="flex gap-3 pt-1">
                  <Button type="button" variant="secondary" onClick={() => setStep("account")}>Back</Button>
                  <Button
                    className="flex-1"
                    disabled={!domain || domainCheck !== "ok"}
                    loading={submitLoading}
                    onClick={handleDomainNext}
                  >
                    {cfToken ? "Save & Configure SSL →" : "Next →"}
                  </Button>
                </div>
              </div>
            </>
          )}

          {/* NGINX */}
          {step === "nginx" && (
            <>
              <div className="px-6 py-4 border-b border-border">
                <h2 className="font-semibold">Configure nginx + SSL</h2>
                <p className="text-xs text-muted-foreground mt-0.5">
                  Auto-configure HTTPS for <strong>{finalDomain}</strong>
                  {cfToken && <span className="text-success ml-1">via DNS-01 — no port 80 needed</span>}
                </p>
              </div>
              <div className="p-6 space-y-4">
                <div className="p-4 bg-muted/50 rounded-xl text-sm text-muted-foreground space-y-1.5">
                  <p className="font-medium text-foreground">Vector will:</p>
                  <p>• Write an nginx reverse proxy config for {finalDomain}</p>
                  {cfToken
                    ? <p>• Run Certbot with <span className="text-foreground font-medium">DNS-01 challenge</span> via Cloudflare (no port 80 needed)</p>
                    : <p>• Run Certbot with HTTP-01 challenge (port 80 must be free)</p>
                  }
                  <p>• Redirect all HTTP → HTTPS automatically</p>
                </div>

                {!nginxLog && (
                  <Button className="w-full" onClick={handleNginx} loading={nginxLoading}>
                    Configure nginx + Get SSL Certificate
                  </Button>
                )}

                {nginxLog && (
                  <pre className="text-xs bg-muted rounded-lg p-3 overflow-auto max-h-52 whitespace-pre-wrap font-mono leading-relaxed">
                    {nginxLog}
                  </pre>
                )}

                {nginxDone && (
                  <Button className="w-full" onClick={() => setStep("done")}>
                    <Check className="w-4 h-4" /> Continue to Dashboard →
                  </Button>
                )}

                {nginxLog && !nginxDone && (
                  <Button variant="secondary" className="w-full" onClick={handleNginx} loading={nginxLoading}>
                    Retry nginx + SSL configuration
                  </Button>
                )}
              </div>
            </>
          )}

          {/* DONE */}
          {step === "done" && (
            <div className="p-7 text-center">
              <div className="w-16 h-16 rounded-full bg-success/15 flex items-center justify-center mx-auto mb-5">
                <Check className="w-8 h-8 text-success" />
              </div>
              <h1 className="text-xl font-bold mb-2">Setup complete!</h1>
              <p className="text-muted-foreground text-sm mb-6">
                {finalDomain
                  ? `Visit https://${finalDomain} to log in. Direct IP access is now blocked.`
                  : "Vector is ready."}
              </p>
              {finalDomain ? (
                <a href={`https://${finalDomain}`}>
                  <Button className="w-full" size="lg">Open https://{finalDomain} →</Button>
                </a>
              ) : (
                <Button className="w-full" size="lg" onClick={() => navigate("/login")}>Go to Login →</Button>
              )}
            </div>
          )}
        </div>

        <p className="text-center text-xs text-muted-foreground mt-5">Vector · Self-hosted URL Shortener</p>
      </div>
    </div>
  );
}
