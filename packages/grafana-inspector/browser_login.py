"""Trusted Grafana form-login adapter executed inside the browser helper."""

def run(credentials, config):
    import subprocess
    import time
    import re
    from urllib.parse import urlsplit

    try:
        from playwright.sync_api import sync_playwright
        private = config["__private"]
        start = urlsplit(config["url"])
        login = urlsplit(config["login_origin"])
        if (start.scheme != "https" or not start.hostname or start.username or start.password
                or login.scheme != "https" or not login.hostname or login.username or login.password
                or login.path not in ("", "/") or login.query or login.fragment):
            return {"status": "interaction_required"}
        expected = (login.scheme, login.hostname.lower(), login.port or 443)
        ca = config.get("ca_pem", "")
        if ca:
            subprocess.run(["certutil", "-d", "sql:/home/kasm-user/.pki/nssdb", "-A", "-t", "C,,", "-n", "aact-grafana-ca"], input=ca.encode(), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10, check=True)
        with sync_playwright() as playwright:
            browser = None
            for attempt in range(10):
                try:
                    browser = playwright.chromium.connect_over_cdp("http://127.0.0.1:9222", timeout=1000)
                    break
                except Exception:
                    time.sleep(0.5)
            if browser is None:
                return {"status": "interaction_required"}
            context = browser.contexts[0]
            def session():
                cookies = context.cookies([config["url"]])
                refresh_name = config.get("refresh_cookie_name", "")
                result = {item["name"]: item["value"] for item in cookies if item["name"] in ("grafana_session", "grafana_session_expiry", refresh_name)}
                if refresh_name and not result.get(refresh_name):
                    return None
                if result.get("grafana_session") and result.get("grafana_session_expiry"):
                    affinity = [{"name": c["name"], "value": c["value"], "domain": c["domain"], "path": "/", "secure": True, "domain_specified": c["domain"].startswith("."), "expires": None, "http_only": True} for c in cookies if re.fullmatch(r"[0-9a-f]{32}", c["name"]) and c["domain"].lstrip(".").lower() == start.hostname.lower() and c["path"] == "/" and c["secure"] and c["httpOnly"] and c["expires"] == -1]
                    output = {"status": "refreshed", "grafana_session": result["grafana_session"], "session_expiry": result["grafana_session_expiry"], "route_cookies": affinity}
                    if refresh_name:
                        output["oauth_refresh"] = result[refresh_name]
                    return output
                return None
            if config.get("interaction_pending"):
                return session() or {"status": "interaction_required"}
            context.clear_cookies(name="grafana_session")
            context.clear_cookies(name="grafana_session_expiry")
            page = context.new_page()
            page.goto(config["url"], wait_until="domcontentloaded", timeout=15000)
            current = urlsplit(page.url)
            if (current.scheme, (current.hostname or "").lower(), current.port or 443) == (start.scheme, start.hostname.lower(), start.port or 443):
                return session() or {"status": "interaction_required"}
            if (current.scheme, (current.hostname or "").lower(), current.port or 443) != expected:
                return {"status": "interaction_required"}
            page.wait_for_selector(config["password_selector"], timeout=5000)
            with page.expect_navigation(wait_until="domcontentloaded", timeout=10000):
                submitted = page.evaluate("""({origin, userSelector, passwordSelector, submitSelector, username, password}) => {
                if (location.origin !== origin) return false;
                const user = document.querySelectorAll(userSelector), pass = document.querySelectorAll(passwordSelector), submit = document.querySelectorAll(submitSelector);
                if (user.length !== 1 || pass.length !== 1 || submit.length !== 1 || !(user[0] instanceof HTMLInputElement) || !(pass[0] instanceof HTMLInputElement)) return false;
                const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
                for (const [element, value] of [[user[0], username], [pass[0], password]]) { set.call(element, value); element.dispatchEvent(new Event('input', {bubbles:true})); element.dispatchEvent(new Event('change', {bubbles:true})); }
                submit[0].click(); return true;
                }""", {"origin": config["login_origin"].rstrip("/"), "userSelector": config["username_selector"], "passwordSelector": config["password_selector"], "submitSelector": config["submit_selector"], "username": config["username"], "password": private["password"]})
            if not submitted:
                return {"status": "interaction_required"}
            page.wait_for_url(lambda value: (urlsplit(value).scheme, (urlsplit(value).hostname or "").lower(), urlsplit(value).port or 443) == (start.scheme, start.hostname.lower(), start.port or 443), timeout=10000)
            return session() or {"status": "interaction_required"}
    except Exception:
        return {"status": "interaction_required"}
