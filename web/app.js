(function () {
    "use strict";

    var token = null;
    var currentSession = null;
    var pollInterval = null;
    var activeScreen = "home";
    var destSelections = {};
    var androidSyncActive = false;

    // --- API helper ---

    function api(method, path, body) {
        var opts = {
            method: method,
            headers: {}
        };
        if (token) {
            opts.headers["X-Presync-Token"] = token;
        }
        if (body !== undefined) {
            opts.headers["Content-Type"] = "application/json";
            opts.body = JSON.stringify(body);
        }
        return fetch(path, opts).then(function (res) {
            return res.json().then(function (data) {
                if (!res.ok) {
                    return Promise.reject(data);
                }
                return data;
            });
        });
    }

    // --- Router ---

    function getHash() {
        var hash = location.hash.replace(/^#\/?/, "");
        return hash || "home";
    }

    function navigate(screen) {
        location.hash = "#" + screen;
    }

    function onHashChange() {
        var screen = getHash();
        if (screen === activeScreen) return;
        render(screen);
    }

    function render(screen) {
        activeScreen = screen;
        updateNav(screen);

        if (pollInterval) {
            clearInterval(pollInterval);
            pollInterval = null;
        }

        var content = document.getElementById("content");
        switch (screen) {
            case "progress":
                renderProgress(content);
                break;
            case "profiles":
                renderProfiles(content);
                break;
            case "settings":
                renderSettings(content);
                break;
            default:
                renderHome(content);
                break;
        }
    }

    // --- Nav ---

    function updateNav(screen) {
        var items = document.querySelectorAll(".nav-item");
        for (var i = 0; i < items.length; i++) {
            var item = items[i];
            if (item.getAttribute("data-tab") === screen) {
                item.classList.add("active");
            } else {
                item.classList.remove("active");
            }
        }
    }

    function showProgressTab(show) {
        var tab = document.getElementById("nav-progress");
        if (tab) {
            tab.style.display = show ? "" : "none";
        }
    }

    function setProgressPulse(active) {
        var tab = document.getElementById("nav-progress");
        if (!tab) return;
        var existing = tab.querySelector(".pulse");
        if (active && !existing) {
            var dot = document.createElement("span");
            dot.className = "pulse";
            tab.appendChild(dot);
        } else if (!active && existing) {
            existing.remove();
        }
    }

    // --- Screens ---

    function renderHome(el) {
        el.innerHTML = '<h1>presync</h1><div class="loading">Loading...</div>';

        api("GET", "/profiles/detail").then(function (data) {
            var profiles = data.profiles || [];
            if (profiles.length === 0) {
                el.innerHTML =
                    '<h1>presync</h1>' +
                    '<div class="card"><div class="empty-state">' +
                    "<p>No sync profiles yet</p>" +
                    '<p>Add profiles to config.toml and restart the daemon</p>' +
                    "</div></div>";
                return;
            }

            // Initialize selections: all selected by default
            for (var i = 0; i < profiles.length; i++) {
                var p = profiles[i];
                if (!destSelections[p.name] && p.destinations) {
                    destSelections[p.name] = Object.keys(p.destinations);
                }
            }

            var html = '<h1>presync</h1>';
            var androidBase = isAndroid() ? (window.AndroidBridge.getDestinationDisplayPath() || "") : "";
            for (var i = 0; i < profiles.length; i++) {
                var p = profiles[i];
                var chips = "";
                if (!isAndroid() && p.destinations) {
                    var keys = Object.keys(p.destinations);
                    var singleDest = keys.length === 1;
                    chips = '<div class="dest-chips" data-profile="' + esc(p.name) + '">';
                    for (var j = 0; j < keys.length; j++) {
                        var k = keys[j];
                        var selected = destSelections[p.name] && destSelections[p.name].indexOf(k) !== -1;
                        var cls = "dest-chip";
                        if (selected) cls += " selected";
                        if (singleDest) cls += " locked";
                        chips += '<span class="' + cls + '" data-dest="' + esc(k) + '" title="' + esc(p.destinations[k]) + '">' + esc(k) + "</span>";
                    }
                    chips += "</div>";
                }
                var destSubtitle = "";
                if (isAndroid()) {
                    if (androidBase) {
                        destSubtitle = '<div class="card-subtitle">Sync folder: ' + esc(androidBase + "/" + p.name) + '</div>';
                    } else {
                        destSubtitle = '<div class="card-subtitle" style="color:var(--status-warning)">No folder selected — go to Settings</div>';
                    }
                }
                html +=
                    '<div class="card">' +
                    '<div class="card-title">' + esc(p.name) + "</div>" +
                    '<div class="card-subtitle">' + esc(p.source) + "</div>" +
                    destSubtitle +
                    chips +
                    '<div style="display:flex;justify-content:space-between;align-items:center;margin-top:var(--space-2)">' +
                    '<span class="mode-badge">' + esc(p.mode) + "</span>" +
                    '<button class="btn btn-primary btn-sm" data-profile="' + esc(p.name) + '">Sync Now</button>' +
                    "</div></div>";
            }

            el.innerHTML = html;

            // Bind chip click handlers
            var chipContainers = el.querySelectorAll(".dest-chips");
            for (var c = 0; c < chipContainers.length; c++) {
                var container = chipContainers[c];
                var profileName = container.getAttribute("data-profile");
                var chips = container.querySelectorAll(".dest-chip");
                for (var k = 0; k < chips.length; k++) {
                    chips[k].addEventListener("click", function () {
                        if (this.classList.contains("locked")) return;
                        var dest = this.getAttribute("data-dest");
                        toggleDest(profileName, dest, this);
                    });
                }
            }

            // Bind sync button handlers
            var buttons = el.querySelectorAll("button[data-profile]");
            for (var j = 0; j < buttons.length; j++) {
                buttons[j].addEventListener("click", function () {
                    triggerSync(this.getAttribute("data-profile"), this);
                });
            }
        }).catch(function () {
            el.innerHTML =
                '<h1>presync</h1>' +
                '<div class="card"><div class="empty-state">' +
                "<p>Failed to load profiles</p>" +
                '<p>Is the daemon running? (presync start)</p>' +
                "</div></div>";
        });
    }

    function toggleDest(profileName, dest, chipEl) {
        if (!destSelections[profileName]) return;
        var idx = destSelections[profileName].indexOf(dest);
        if (idx === -1) {
            destSelections[profileName].push(dest);
            chipEl.classList.add("selected");
        } else {
            destSelections[profileName].splice(idx, 1);
            chipEl.classList.remove("selected");
        }
    }

    function triggerSync(profileName, btn) {
        var selected = destSelections[profileName] || [];
        if (selected.length === 0) {
            alert("Select at least one destination");
            return;
        }

        btn.disabled = true;
        btn.textContent = "Starting...";

        if (isAndroid()) {
            syncToDevice(profileName, btn);
        } else {
            api("POST", "/web/sync", { profile: profileName, destinations: selected }).then(function (data) {
                currentSession = data.session;
                showProgressTab(true);
                setProgressPulse(true);
                navigate("progress");
            }).catch(function (err) {
                btn.disabled = false;
                btn.textContent = "Sync Now";
                alert(err.error || "Failed to start sync");
            });
        }
    }

    function syncToDevice(profileName, btn) {
        var deviceId = window.AndroidBridge.getDeviceId() || null;
        if (!deviceId) {
            api("GET", "/web/device-id").then(function (data) {
                deviceId = data.device_id;
                window.AndroidBridge.saveDeviceId(deviceId);
                doSyncToDevice(profileName, btn, deviceId);
            }).catch(function (err) {
                btn.disabled = false;
                btn.textContent = "Sync Now";
                alert(err.error || "Failed to get device ID");
            });
        } else {
            doSyncToDevice(profileName, btn, deviceId);
        }
    }

    function doSyncToDevice(profileName, btn, deviceId) {
        var body = {
            device_id: deviceId,
            profile: profileName,
            destination_manifest: { files: [] }
        };

        var opts = {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "X-Presync-Token": token
            },
            body: JSON.stringify(body)
        };

        var abortController = null;
        if (typeof AbortController !== "undefined") {
            abortController = new AbortController();
            opts.signal = abortController.signal;
            setTimeout(function () { abortController.abort(); }, 30000);
        }

        fetch("/sync", opts).then(function (res) {
            var ct = res.headers.get("content-type") || "";
            if (ct.indexOf("json") === -1) {
                return res.text().then(function (t) {
                    return Promise.reject({ error: "Server returned non-JSON (" + res.status + "): " + t.substring(0, 200) });
                });
            }
            return res.json().then(function (data) {
                if (!res.ok) return Promise.reject(data);
                return data;
            });
        }).then(function (startResp) {
            var sessionPath = startResp.session;
            var files = startResp.files || [];
            var done = 0;
            var errors = 0;

            if (files.length === 0) {
                btn.disabled = false;
                btn.textContent = "Sync Now";
                alert("Already up to date");
                return;
            }

            showProgressTab(true);
            setProgressPulse(true);
            androidSyncActive = true;
            navigate("progress");
            updateSyncProgress(profileName, 0, files.length, 0);

            function syncNext() {
                if (done + errors >= files.length) {
                    fetch("/sync/complete", {
                        method: "POST",
                        headers: {
                            "Content-Type": "application/json",
                            "X-Presync-Token": token
                        },
                        body: JSON.stringify({ session: sessionPath })
                    }).then(function () {
                        androidSyncActive = false;
                        setProgressPulse(false);
                        updateSyncProgress(profileName, done, files.length, errors);
                    }).catch(function () {
                        androidSyncActive = false;
                        setProgressPulse(false);
                    });
                    return;
                }

                var f = files[done + errors];
                updateSyncProgress(profileName, done, files.length, errors);

                fetch("/sync/file?session=" + encodeURIComponent(sessionPath) + "&path=" + encodeURIComponent(f.path), {
                    headers: { "X-Presync-Token": token }
                }).then(function (res) {
                    if (!res.ok) throw new Error("fetch failed");
                    var sha256 = res.headers.get("X-Presync-SHA256");
                    return res.arrayBuffer().then(function (buf) {
                        var bytes = new Uint8Array(buf);
                        var binary = "";
                        for (var i = 0; i < bytes.length; i++) {
                            binary += String.fromCharCode(bytes[i]);
                        }
                        var b64 = btoa(binary);
                        return { b64: b64, sha256: sha256, path: f.path };
                    });
                }).then(function (info) {
                    var callbackName = "_sync_cb_" + Date.now();
                    window[callbackName] = function (result) {
                        delete window[callbackName];
                        if (result.success) {
                            var confirmBody = JSON.stringify({
                                session: sessionPath,
                                path: info.path,
                                sha256: info.sha256
                            });
                            fetch("/sync/confirm", {
                                method: "POST",
                                headers: {
                                    "Content-Type": "application/json",
                                    "X-Presync-Token": token
                                },
                                body: confirmBody
                            }).then(function (res) {
                                return res.json();
                            }).then(function (confirmResult) {
                                if (confirmResult.ok) {
                                    done++;
                                } else {
                                    errors++;
                                }
                                syncNext();
                            }).catch(function () {
                                errors++;
                                syncNext();
                            });
                        } else {
                            errors++;
                            syncNext();
                        }
                    };
                    window.AndroidBridge.writeFile(profileName + "/" + info.path, info.b64, callbackName);
                }).catch(function () {
                    errors++;
                    syncNext();
                });
            }

            syncNext();
        }).catch(function (err) {
            btn.disabled = false;
            btn.textContent = "Sync Now";
            alert(err.error || "Failed to start sync");
        });
    }

    function updateSyncProgress(profileName, done, total, errors) {
        var pct = total > 0 ? Math.round((done / total) * 100) : 0;
        var el = document.getElementById("content");
        if (activeScreen !== "progress") return;
        el.innerHTML =
            '<h1>Sync Progress</h1>' +
            '<div class="card">' +
            '<div class="card-title">' + esc(profileName) + "</div>" +
            '<div class="card-subtitle">' + done + " of " + total + " files" +
            (errors > 0 ? ' &middot; <span style="color:var(--status-error)">' + errors + " errors</span>" : "") +
            "</div>" +
            '<div class="progress-bar" style="margin-top:var(--space-2)">' +
            '<div class="progress-fill" style="width:' + pct + '%"></div>' +
            "</div>" +
            "</div>";
    }

    function renderProgress(el) {
        if (androidSyncActive) return;
        el.innerHTML = '<h1>Sync Progress</h1><div class="loading">Loading...</div>';

        var lastDone = -1;
        var lastErrors = -1;
        var stallCount = 0;

        function refresh() {
            api("GET", "/web/sessions").then(function (data) {
                var sessions = data.sessions || [];
                var active = null;
                for (var i = 0; i < sessions.length; i++) {
                    if (!sessions[i].complete) {
                        active = sessions[i];
                        break;
                    }
                }

                if (!active) {
                    showProgressTab(false);
                    setProgressPulse(false);
                    if (currentSession) {
                        el.innerHTML =
                            '<h1>Sync Progress</h1>' +
                            '<div class="card"><div class="empty-state">' +
                            "<p>Sync complete</p>" +
                            "</div></div>";
                    } else {
                        el.innerHTML =
                            '<h1>Sync Progress</h1>' +
                            '<div class="card"><div class="empty-state">' +
                            "<p>No active sync</p>" +
                            '<p>Start a sync from the Home screen</p>' +
                            "</div></div>";
                    }
                    stallCount = 0;
                    return;
                }

                if (active.done === lastDone && active.errors === lastErrors) {
                    stallCount++;
                } else {
                    stallCount = 0;
                }
                lastDone = active.done;
                lastErrors = active.errors;

                var stalled = stallCount >= 10;
                showProgressTab(!stalled);
                setProgressPulse(!stalled);

                var pct = active.total > 0 ? Math.round((active.done / active.total) * 100) : 0;

                var html =
                    '<h1>Sync Progress</h1>' +
                    '<div class="card">' +
                    '<div class="card-title">' + esc(active.profile) + "</div>" +
                    '<div class="card-subtitle">' + active.done + " of " + active.total + " files" +
                    (active.errors > 0 ? ' &middot; <span style="color:var(--status-error)">' + active.errors + " errors</span>" : "") +
                    (stalled ? ' <span style="color:var(--status-warning)">&middot; appears stalled</span>' : "") +
                    "</div>" +
                    '<div class="progress-bar" style="margin-top:var(--space-2)">' +
                    '<div class="progress-fill" style="width:' + pct + '%"></div>' +
                    "</div>" +
                    "</div>";

                el.innerHTML = html;
            }).catch(function () {
                el.innerHTML =
                    '<h1>Sync Progress</h1>' +
                    '<div class="card"><div class="empty-state">' +
                    "<p>Failed to load sync status</p>" +
                    "</div></div>";
            });
        }

        refresh();
        pollInterval = setInterval(refresh, 1000);
    }

    function renderProfiles(el) {
        el.innerHTML = '<h1>Profiles</h1><div class="loading">Loading...</div>';

        api("GET", "/profiles/detail").then(function (data) {
            var profiles = data.profiles || [];
            if (profiles.length === 0) {
                el.innerHTML =
                    '<h1>Profiles</h1>' +
                    '<div class="card"><div class="empty-state">' +
                    "<p>No profiles configured</p>" +
                    '<p>Add profiles to config.toml and restart the daemon</p>' +
                    "</div></div>";
                return;
            }

            var androidBase = "";
            if (isAndroid()) {
                androidBase = window.AndroidBridge.getDestinationDisplayPath() || "";
            }

            var html = '<h1>Profiles</h1>';
            for (var i = 0; i < profiles.length; i++) {
                var p = profiles[i];
                html +=
                    '<div class="card">' +
                    '<div class="card-title">' + esc(p.name) + "</div>" +
                    '<div class="setting-row"><span class="setting-label">Source</span><span class="setting-value">' + esc(p.source) + "</span></div>";
                if (isAndroid()) {
                    if (androidBase) {
                        html += '<div class="setting-row"><span class="setting-label">Destination</span><span class="setting-value">' + esc(androidBase + "/" + p.name) + "</span></div>";
                    } else {
                        html += '<div class="setting-row"><span class="setting-label">Destination</span><span class="setting-value" style="color:var(--status-warning)">No folder selected</span></div>';
                    }
                } else if (p.destinations) {
                    var keys = Object.keys(p.destinations);
                    for (var j = 0; j < keys.length; j++) {
                        html += '<div class="setting-row"><span class="setting-label">Dest: ' + esc(keys[j]) + '</span><span class="setting-value">' + esc(p.destinations[keys[j]]) + "</span></div>";
                    }
                }
                html +=
                    '<div class="setting-row"><span class="setting-label">Mode</span><span class="mode-badge">' + esc(p.mode) + "</span></div>" +
                    "</div>";
            }
            el.innerHTML = html;
        }).catch(function () {
            el.innerHTML =
                '<h1>Profiles</h1>' +
                '<div class="card"><div class="empty-state">' +
                "<p>Failed to load profiles</p>" +
                "</div></div>";
        });
    }

    function renderSettings(el) {
        el.innerHTML = '<h1>Settings</h1><div class="loading">Loading...</div>';

        api("GET", "/status").then(function (data) {
            var html =
                '<h1>Settings</h1>' +
                '<div class="card">' +
                '<div class="card-title">Server</div>' +
                '<div class="setting-row"><span class="setting-label">Port</span><span class="setting-value">' + data.port + "</span></div>" +
                '<div class="setting-row"><span class="setting-label">Paired devices</span><span class="setting-value">' + data.devices + "</span></div>" +
                "</div>" +
                '<div class="card">' +
                '<div class="card-title">Sync</div>' +
                '<div class="setting-row"><span class="setting-label">Auto-sync</span><div class="toggle" id="toggle-autosync"></div></div>' +
                "</div>" +
                '<div class="card">' +
                '<div class="card-title">This Device</div>' +
                '<div id="forget-server-block"></div>' +
                "</div>";

            el.innerHTML = html;

            var toggle = document.getElementById("toggle-autosync");
            if (toggle) {
                toggle.addEventListener("click", function () {
                    this.classList.toggle("on");
                });
            }

            var forgetBlock = document.getElementById("forget-server-block");
            if (forgetBlock) {
                if (isAndroid()) {
                    var destPath = window.AndroidBridge.getDestinationDisplayPath() || "";
                    var destStatus = destPath
                        ? '<p class="card-subtitle">Sync folder: <strong>' + esc(destPath) + '</strong></p>'
                        : '<p class="card-subtitle" style="color:var(--status-warning)">No sync folder selected — tap Change to pick one</p>';
                    forgetBlock.innerHTML =
                        destStatus +
                        '<div style="display:flex;gap:var(--space-2);margin:var(--space-2) 0">' +
                        '<button class="btn btn-secondary btn-sm" id="btn-change-folder">Change folder</button>' +
                        '<button class="btn btn-secondary btn-sm" id="btn-forget-server">Forget this server</button>' +
                        "</div>";
                    document.getElementById("btn-change-folder").addEventListener("click", function () {
                        window.onDestinationPicked = function (result) {
                            if (result.success) {
                                var newPath = window.AndroidBridge.getDestinationDisplayPath();
                                var sub = forgetBlock.querySelector(".card-subtitle");
                                if (sub) {
                                    sub.innerHTML = 'Sync folder: <strong>' + esc(newPath) + '</strong>';
                                    sub.style.color = "";
                                }
                            }
                        };
                        window.AndroidBridge.pickDestinationFolder();
                    });
                    document.getElementById("btn-forget-server").addEventListener("click", function () {
                        if (confirm("Forget this server and remove all stored data?")) {
                            window.AndroidBridge.clearServerData();
                        }
                    });
                } else {
                    var destHtml = '<p class="card-subtitle">Destinations are configured in server config or via <code>presync connect</code>.</p>';
                    forgetBlock.innerHTML =
                        destHtml +
                        '<div style="margin-top:var(--space-2)">' +
                        '<p class="card-subtitle">To unpair, run <code>presync unpair</code> on the server.</p>' +
                        "</div>";
                }
            }
        }).catch(function () {
            var serverUnreachable =
                '<h1>Settings</h1>' +
                '<div class="card"><div class="empty-state">' +
                "<p>Failed to load settings</p>" +
                "</div></div>";

            if (isAndroid()) {
                serverUnreachable +=
                    '<div class="card" style="margin-top:var(--space-3)">' +
                    '<div class="card-title">Server unreachable?</div>' +
                    '<p class="card-subtitle" style="margin-bottom:var(--space-2)">If the server address has changed, forget it and re-pair.</p>' +
                    '<button class="btn btn-secondary" id="btn-forget-server-alt">Forget this server</button>' +
                    "</div>";
            }

            el.innerHTML = serverUnreachable;

            var forgetBtn = document.getElementById("btn-forget-server-alt");
            if (forgetBtn) {
                forgetBtn.addEventListener("click", function () {
                    if (confirm("Forget this server and remove all stored data?")) {
                        window.AndroidBridge.clearServerData();
                    }
                });
            }
        });
    }

    // --- Helpers ---

    function esc(s) {
        var div = document.createElement("div");
        div.textContent = s;
        return div.innerHTML;
    }

    // --- Pairing ---

    function getParam(name) {
        var match = RegExp("[?&]" + name + "=([^&]*)").exec(location.search);
        return match ? decodeURIComponent(match[1]) : null;
    }

    function isAndroid() {
        return typeof window.AndroidBridge !== "undefined";
    }

    function renderPairing(el, sessionToken) {
        el.innerHTML =
            '<h1>Pair with Server</h1>' +
            '<div class="card">' +
            '<div class="card-title">Confirm pairing</div>' +
            '<div class="card-subtitle">Enter a name for this device</div>' +
            '<input type="text" id="pair-device-name" class="input" placeholder="Device name" style="margin-top:var(--space-2);width:100%">' +
            '<div id="pair-pin-area" style="margin-top:var(--space-3);display:none">' +
            '<div class="card-subtitle">PIN displayed on server:</div>' +
            '<div id="pair-pin" style="font-size:32px;font-weight:600;letter-spacing:4px;margin:var(--space-2) 0;color:var(--accent-base)"></div>' +
            '<div class="card-subtitle">Does this match?</div>' +
            '</div>' +
            '<div id="pair-error" class="error-text" style="margin-top:var(--space-2)"></div>' +
            '<div style="margin-top:var(--space-3)">' +
            '<button class="btn btn-primary" id="pair-confirm-btn" disabled>Confirm Pairing</button>' +
            '</div>' +
            '</div>';

        var confirmBtn = document.getElementById("pair-confirm-btn");
        var nameInput = document.getElementById("pair-device-name");
        var pinArea = document.getElementById("pair-pin-area");
        var pinDisplay = document.getElementById("pair-pin");
        var errorEl = document.getElementById("pair-error");

        var deviceName = isAndroid() ? "android" : (navigator.userAgent || "web-client").split(" ")[0];
        nameInput.value = deviceName;

        function apiCall(method, path, body) {
            var opts = { method: method, headers: {} };
            if (body !== undefined) {
                opts.headers["Content-Type"] = "application/json";
                opts.body = JSON.stringify(body);
            }
            return fetch(path, opts).then(function (res) {
                return res.json().then(function (data) {
                    if (!res.ok) return Promise.reject(data);
                    return data;
                });
            });
        }

        nameInput.addEventListener("input", function () {
            confirmBtn.disabled = !this.value.trim();
        });

        confirmBtn.addEventListener("click", function () {
            var name = nameInput.value.trim();
            if (!name) return;

            errorEl.textContent = "";
            confirmBtn.disabled = true;
            confirmBtn.textContent = "Connecting...";

            apiCall("POST", "/pair/accept", { token: sessionToken, name: name }).then(function (data) {
                pinArea.style.display = "";
                pinDisplay.textContent = data.pin;
                confirmBtn.textContent = "Waiting for server...";
                confirmBtn.disabled = true;

                // Poll for server-side confirmation (CLI calls /pair/confirm)
                var pollCount = 0;
                var pollInterval = setInterval(function () {
                    pollCount++;
                    if (pollCount > 60) {
                        clearInterval(pollInterval);
                        errorEl.textContent = "Timed out waiting for server confirmation";
                        confirmBtn.textContent = "Confirm Pairing";
                        confirmBtn.disabled = false;
                        return;
                    }
                    apiCall("GET", "/pair/confirmed?token=" + encodeURIComponent(sessionToken)).then(function (result) {
                        if (result.confirmed) {
                            clearInterval(pollInterval);
                            token = result.token;

                            var goHome = function () {
                                history.replaceState(null, "", location.pathname);
                                window.addEventListener("hashchange", onHashChange);
                                render("home");
                            };

                            if (isAndroid()) {
                                window.AndroidBridge.saveToken(result.token);
                                window.AndroidBridge.saveServerUrl(location.origin);
                                if (result.device_id) {
                                    window.AndroidBridge.saveDeviceId(result.device_id);
                                }

                                confirmBtn.textContent = "Pick a folder\u2026";
                                pinDisplay.textContent = "Paired!";
                                pinDisplay.style.color = "var(--status-success)";

                                window.onDestinationPicked = function (pickResult) {
                                    if (pickResult.success) {
                                        goHome();
                                    } else {
                                        pinDisplay.textContent = "No folder selected";
                                        pinDisplay.style.color = "var(--status-warning)";
                                        setTimeout(goHome, 1500);
                                    }
                                };

                                window.AndroidBridge.pickDestinationFolder();
                            } else {
                                localStorage.setItem("presync_token", result.token);
                                if (result.device_id) {
                                    localStorage.setItem("presync_device_id", result.device_id);
                                }
                                goHome();
                            }
                        }
                    }).catch(function () {
                        // ignore poll errors — keep waiting
                    });
                }, 1000);
            }).catch(function (err) {
                errorEl.textContent = err.error || "Failed to connect to server";
                confirmBtn.textContent = "Confirm Pairing";
                confirmBtn.disabled = false;
            });
        });

        if (nameInput.value) confirmBtn.disabled = false;
    }

    // --- Init ---

    function init() {
        var sessionToken = getParam("token");

        if (sessionToken) {
            renderPairing(document.getElementById("content"), sessionToken);
            return;
        }

        var storedToken = null;
        if (isAndroid()) {
            storedToken = window.AndroidBridge.getToken() || null;
        } else {
            storedToken = localStorage.getItem("presync_token");
        }

        if (storedToken) {
            token = storedToken;
            window.addEventListener("hashchange", onHashChange);
            render(getHash());
        } else {
            api("GET", "/web/token").then(function (data) {
                token = data.token;
                window.addEventListener("hashchange", onHashChange);
                render(getHash());
            }).catch(function () {
                document.getElementById("content").innerHTML =
                    '<div class="card"><div class="empty-state">' +
                    "<p>Failed to connect to server</p>" +
                    '<p>Is the daemon running? (presync start)</p>' +
                    "</div></div>";
            });
        }
    }

    if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", init);
    } else {
        init();
    }
})();
