(function () {
    "use strict";

    var token = null;
    var pollInterval = null;
    var activeScreen = "home";
    var destSelections = {};
    var androidSyncActive = false;
    var androidSyncStopRequested = false;
    var androidSyncCanStop = false;
    var androidSyncAbort = null;
    var androidSyncProfile = "";
    var androidSyncDone = 0;
    var androidSyncTotal = 0;
    var androidSyncErrors = 0;
    var androidSyncCurrent = "";
    var androidSyncDirection = "pull";
    var pausedSession = null;
    var progressSessions = [];
    var progressCompleted = null;
    var bridgeCallbackSeq = 0;

    // Android's JavascriptInterface can only receive strings, not functions.
    // Each callback is registered on window under a generated name and the
    // name is passed to the bridge; invokeCallback evaluates it by name.
    function bridgeCallback(fn) {
        var name = "__presyncBridgeCb" + (++bridgeCallbackSeq);
        window[name] = function (result) {
            delete window[name];
            fn(result);
        };
        return name;
    }

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

    // Toggles the Android-side keep-screen-on flag along with the JS state.
    // Older APKs without the bridge method still sync, they just may sleep.
    function setAndroidSyncActive(active) {
        androidSyncActive = active;
        if (isAndroid() && typeof window.AndroidBridge.setSyncActive === "function") {
            try {
                window.AndroidBridge.setSyncActive(active);
            } catch (e) {
                // Ignore — screen wake is a convenience, not a sync requirement.
            }
        }
    }

    function requestAndroidSyncStop() {
        androidSyncStopRequested = true;
        if (androidSyncAbort) {
            try {
                androidSyncAbort.abort();
            } catch (e) {
                // The loop also checks the flag between chunks.
            }
            androidSyncAbort = null;
        }
    }

    // Leaves the session and temp files in place, so Resume continues it.
    // The stop flag stays set until the next sync starts, so a late callback
    // from the interrupted transfer cannot restart the loop.
    function pauseAndroidSync() {
        androidSyncCanStop = false;
        setAndroidSyncActive(false);
        setProgressPulse(false);
        pausedSession = {
            profile: androidSyncProfile,
            direction: androidSyncDirection,
            done: androidSyncDone,
            total: androidSyncTotal,
            errors: androidSyncErrors,
            current: androidSyncCurrent
        };
        showProgressTab(true);
        renderPausedProgress();
        if (activeScreen === "home") applyHomeActions();
    }

    function renderPausedProgress() {
        renderProgressView();
    }

    function sessionKey(profile, direction) {
        return (direction || "pull") + "|" + profile;
    }

    // Progress lists every session at once: the local transfer in flight, the
    // locally paused session, and any other session the server still tracks
    // (a profile stopped earlier, or another paired device viewed from the PC).
    // Deduping by profile+direction keeps the live card from appearing twice.
    function buildProgressCards() {
        var cards = [];
        var seen = {};

        // The server still lists the just-completed session until it is
        // reaped; don't show a stale "paused" card under "Sync complete".
        if (progressCompleted) {
            seen[sessionKey(progressCompleted.profile, progressCompleted.direction)] = true;
        }

        if (androidSyncActive) {
            seen[sessionKey(androidSyncProfile, androidSyncDirection)] = true;
            cards.push({
                profile: androidSyncProfile,
                direction: androidSyncDirection,
                done: androidSyncDone,
                total: androidSyncTotal,
                errors: androidSyncErrors,
                current: androidSyncCurrent,
                running: true,
                canStop: androidSyncCanStop,
                tone: ""
            });
        }

        if (pausedSession) {
            var pk = sessionKey(pausedSession.profile, pausedSession.direction);
            if (!seen[pk]) {
                seen[pk] = true;
                cards.push({
                    profile: pausedSession.profile,
                    direction: pausedSession.direction,
                    done: pausedSession.done,
                    total: pausedSession.total,
                    errors: pausedSession.errors,
                    current: pausedSession.current,
                    running: false,
                    canResume: true,
                    tone: "paused"
                });
            }
        }

        for (var i = 0; i < progressSessions.length; i++) {
            var s = progressSessions[i];
            if (s.complete) continue;
            var k = sessionKey(s.profile, s.direction);
            if (seen[k]) continue;
            seen[k] = true;
            cards.push({
                profile: s.profile,
                direction: s.direction,
                done: s.done,
                total: s.total,
                errors: s.errors,
                current: s.current,
                running: false,
                canResume: isAndroid(),
                tone: isAndroid() ? "paused" : "external"
            });
        }

        return cards;
    }

    function progressCardHTML(card, index) {
        var total = card.total || 0;
        var done = card.done || 0;
        var errors = card.errors || 0;
        var pct = total > 0 ? Math.round((done / total) * 100) : 0;

        var subtitle = done + " of " + total + " files";
        if (errors > 0) {
            subtitle += ' &middot; <span style="color:var(--status-error)">' + errors + " errors</span>";
        }
        if (card.tone === "paused") {
            subtitle += ' &middot; <span style="color:var(--status-warning)">paused</span>';
        }

        var current = "";
        if (card.current) {
            var stateLabel = card.running ? "sending" : (card.tone === "paused" ? "paused" : "in progress");
            current =
                '<div class="file-list"><div class="file-item">' +
                '<span class="name">' + esc(card.current) + "</span>" +
                '<span class="state in_progress">' + stateLabel + "</span>" +
                "</div></div>";
        }

        var actions = "";
        if (card.running && card.canStop) {
            actions =
                '<div style="margin-top:var(--space-3)">' +
                '<button class="btn btn-secondary" data-card-stop="' + index + '">Stop</button>' +
                "</div>";
        } else if (card.canResume) {
            var label = card.direction === "push" ? "Resume upload" : "Resume sync";
            var disabled = androidSyncActive ? " disabled" : "";
            actions =
                '<div style="margin-top:var(--space-3)">' +
                '<button class="btn btn-primary" data-card-resume="' + index + '"' + disabled + ">" + label + "</button>" +
                "</div>";
        }

        var title = card.complete ? "Sync complete" : esc(card.profile || "");
        return (
            '<div class="card">' +
            '<div class="card-title">' + title + "</div>" +
            '<div class="card-subtitle">' + subtitle + "</div>" +
            '<div class="progress-bar" style="margin-top:var(--space-2)">' +
            '<div class="progress-fill" style="width:' + pct + '%"></div>' +
            "</div>" +
            current +
            actions +
            "</div>"
        );
    }

    function renderProgressView() {
        var el = document.getElementById("content");
        if (!el || activeScreen !== "progress") return;

        var cards = buildProgressCards();
        if (progressCompleted) {
            cards.unshift({
                profile: progressCompleted.profile,
                direction: progressCompleted.direction,
                done: progressCompleted.done,
                total: progressCompleted.total,
                errors: progressCompleted.errors,
                complete: true
            });
        }

        var html = "<h1>Sync Progress</h1>";
        if (cards.length === 0) {
            html +=
                '<div class="card"><div class="empty-state">' +
                "<p>No active sync</p>" +
                '<p>Start a sync from the Home screen</p>' +
                "</div></div>";
        } else {
            for (var i = 0; i < cards.length; i++) {
                html += progressCardHTML(cards[i], i);
            }
        }
        el.innerHTML = html;

        var stopButtons = el.querySelectorAll("button[data-card-stop]");
        for (var s = 0; s < stopButtons.length; s++) {
            stopButtons[s].addEventListener("click", function () {
                this.disabled = true;
                this.textContent = "Stopping\u2026";
                requestAndroidSyncStop();
            });
        }

        var resumeButtons = el.querySelectorAll("button[data-card-resume]");
        for (var r = 0; r < resumeButtons.length; r++) {
            resumeButtons[r].addEventListener("click", function () {
                var card = cards[parseInt(this.getAttribute("data-card-resume"), 10)];
                if (!card) return;
                this.disabled = true;
                this.textContent = "Starting\u2026";
                if (card.direction === "push") {
                    pushToDevice(card.profile, this);
                } else {
                    triggerSync(card.profile, this);
                }
            });
        }
    }

    // Fetches every session the server still tracks and re-renders the list.
    // Runs while the Progress tab is open, including during a local transfer,
    // so a paused session never disappears behind the active one.
    function refreshProgressSessions() {
        api("GET", "/web/sessions").then(function (data) {
            progressSessions = data.sessions || [];
            var has = androidSyncActive || !!pausedSession;
            for (var i = 0; i < progressSessions.length; i++) {
                if (!progressSessions[i].complete) {
                    has = true;
                    break;
                }
            }
            if (!has) {
                setProgressPulse(false);
            }
            showProgressTab(has);
            renderProgressView();
        }).catch(function () {
            if (activeScreen !== "progress") return;
            if (androidSyncActive || pausedSession || progressCompleted || progressSessions.length > 0) {
                renderProgressView();
                return;
            }
            var el = document.getElementById("content");
            if (el) {
                el.innerHTML =
                    "<h1>Sync Progress</h1>" +
                    '<div class="card"><div class="empty-state">' +
                    "<p>Failed to load sync status</p>" +
                    "</div></div>";
            }
        });
    }

    function refreshProgressTab() {
        if (!token || androidSyncActive) return;
        api("GET", "/web/sessions").then(function (data) {
            var sessions = data.sessions || [];
            var has = false;
            for (var i = 0; i < sessions.length; i++) {
                if (!sessions[i].complete) has = true;
            }
            showProgressTab(has);
        }).catch(function () {
            // Leave the tab as-is if the status call fails.
        });
    }

    // Keeps the Home cards honest: the active profile is "Syncing…" and not
    // pressable, resumable sessions say "Resume", and everything else is idle.
    function applyHomeActions() {
        if (activeScreen !== "home") return;
        var el = document.getElementById("content");
        if (!el) return;

        if (isAndroid() && androidSyncActive) {
            var busy = el.querySelectorAll("button[data-profile], button[data-push]");
            for (var i = 0; i < busy.length; i++) {
                busy[i].disabled = true;
                var name = busy[i].getAttribute("data-profile") || busy[i].getAttribute("data-push");
                if (name === androidSyncProfile) {
                    busy[i].textContent = androidSyncDirection === "push" ? "Sending\u2026" : "Syncing\u2026";
                }
            }
        }

        api("GET", "/web/sessions").then(function (data) {
            if (activeScreen !== "home") return;
            var sessions = data.sessions || [];
            var byProfile = {};
            for (var i = 0; i < sessions.length; i++) {
                if (!sessions[i].complete && !byProfile[sessions[i].profile]) {
                    byProfile[sessions[i].profile] = sessions[i];
                }
            }
            var hasAny = false;
            for (var k in byProfile) {
                hasAny = true;
                break;
            }
            if (!androidSyncActive) showProgressTab(hasAny);
            if (!isAndroid()) return;

            var pullButtons = el.querySelectorAll("button[data-profile]");
            for (var i = 0; i < pullButtons.length; i++) {
                var b = pullButtons[i];
                var name = b.getAttribute("data-profile");
                var s = byProfile[name];
                if (androidSyncActive) {
                    b.disabled = true;
                    if (name === androidSyncProfile) {
                        b.textContent = androidSyncDirection === "push" ? "Sending\u2026" : "Syncing\u2026";
                    }
                } else if (s) {
                    b.disabled = false;
                    b.textContent = s.direction === "push" ? "Resume upload" : "Resume sync";
                } else {
                    b.disabled = false;
                    b.textContent = "Sync Now";
                }
            }

            var pushButtons = el.querySelectorAll("button[data-push]");
            for (var i = 0; i < pushButtons.length; i++) {
                var b = pushButtons[i];
                var name = b.getAttribute("data-push");
                var s = byProfile[name];
                if (androidSyncActive) {
                    b.disabled = true;
                    if (name === androidSyncProfile) {
                        b.textContent = androidSyncDirection === "push" ? "Sending\u2026" : "Syncing\u2026";
                    }
                } else if (s) {
                    b.disabled = false;
                    b.textContent = "Resume upload";
                } else {
                    b.disabled = false;
                    b.textContent = "Send to PC";
                }
            }
        }).catch(function () {
            // Leave the cards as rendered if the status call fails.
        });
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
                var isPush = p.direction === "push";
                var chips = "";
                var destSubtitle = "";
                var action = "";
                var mainSubtitle = p.source;

                if (isPush) {
                    mainSubtitle = isAndroid() ? "" : (p.destination || "");
                    if (isAndroid()) {
                        var pushSource = window.AndroidBridge.getPushSourceDisplayPath(p.name) || "";
                        if (pushSource) {
                            destSubtitle = '<div class="card-subtitle">Upload folder: ' + esc(pushSource) + "</div>";
                            action = '<button class="btn btn-primary btn-sm" data-push="' + esc(p.name) + '">Send to PC</button>';
                        } else {
                            destSubtitle = '<div class="card-subtitle" style="color:var(--status-warning)">No upload folder selected — choose one</div>';
                            action = '<button class="btn btn-primary btn-sm" data-pick-push="' + esc(p.name) + '">Choose folder</button>';
                        }
                    } else {
                        destSubtitle = '<div class="card-subtitle">Receives from a paired device</div>';
                    }
                } else {
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

                    if (isAndroid()) {
                        if (androidBase) {
                            destSubtitle = '<div class="card-subtitle">Sync folder: ' + esc(androidBase + "/" + p.name) + '</div>';
                        } else {
                            destSubtitle = '<div class="card-subtitle" style="color:var(--status-warning)">No folder selected — go to Settings</div>';
                        }
                    }
                    action = '<button class="btn btn-primary btn-sm" data-profile="' + esc(p.name) + '">Sync Now</button>';
                }

                var badges = '<span class="mode-badge">' + esc(p.mode) + "</span>";
                if (isPush) badges += '<span class="mode-badge">push</span>';

                html +=
                    '<div class="card">' +
                    '<div class="card-title">' + esc(p.name) + "</div>" +
                    (mainSubtitle ? '<div class="card-subtitle">' + esc(mainSubtitle) + "</div>" : "") +
                    destSubtitle +
                    chips +
                    '<div style="display:flex;justify-content:space-between;align-items:center;margin-top:var(--space-2)">' +
                    '<div style="display:flex;gap:var(--space-1);align-items:center">' + badges + "</div>" +
                    action +
                    "</div></div>";
            }

            el.innerHTML = html;
            applyHomeActions();

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

            // Bind push source picker / send handlers
            var pickButtons = el.querySelectorAll("button[data-pick-push]");
            for (var pb = 0; pb < pickButtons.length; pb++) {
                pickButtons[pb].addEventListener("click", function () {
                    var name = this.getAttribute("data-pick-push");
                    window.onPushSourcePicked = function () {
                        render("home");
                    };
                    window.AndroidBridge.pickPushSourceFolder(name);
                });
            }

            var pushButtons = el.querySelectorAll("button[data-push]");
            for (var sb = 0; sb < pushButtons.length; sb++) {
                pushButtons[sb].addEventListener("click", function () {
                    pushToDevice(this.getAttribute("data-push"), this);
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
        if (androidSyncActive) {
            alert("A sync is already running.");
            return;
        }
        progressCompleted = null;
        btn.disabled = true;
        btn.textContent = "Starting...";

        if (isAndroid()) {
            // Mark busy immediately: the SAF manifest walk can take a moment,
            // and Home must not offer a second start during it.
            androidSyncProfile = profileName;
            androidSyncDirection = "pull";
            androidSyncDone = 0;
            androidSyncTotal = 0;
            androidSyncErrors = 0;
            androidSyncCurrent = "";
            setAndroidSyncActive(true);
            applyHomeActions();
            syncToDevice(profileName, btn);
            return;
        }

        var selected = destSelections[profileName] || [];
        if (selected.length === 0) {
            btn.disabled = false;
            btn.textContent = "Sync Now";
            alert("Select at least one destination");
            return;
        }

        api("POST", "/web/sync", { profile: profileName, destinations: selected }).then(function () {
            showProgressTab(true);
            setProgressPulse(true);
            navigate("progress");
        }).catch(function (err) {
            btn.disabled = false;
            btn.textContent = "Sync Now";
            alert(err.error || "Failed to start sync");
        });
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
        // Build a real destination manifest so the server computes a delta.
        // A missing folder or manifest failure falls back to an empty manifest
        // (full sync) rather than failing the whole sync.
        window.AndroidBridge.buildManifest(profileName, bridgeCallback(function (result) {
            var manifest = { files: [] };
            if (result.success) {
                try {
                    manifest = JSON.parse(result.data);
                } catch (e) {
                    manifest = { files: [] };
                }
            }
            startAndroidSync(profileName, btn, deviceId, manifest);
        }));
    }

    function startAndroidSync(profileName, btn, deviceId, destManifest) {
        var body = {
            device_id: deviceId,
            profile: profileName,
            destination_manifest: destManifest
        };

        var opts = {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "X-Presync-Token": token
            },
            body: JSON.stringify(body)
        };

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
            var delta = startResp.delta || { to_delete: [] };

            // Files already done in a resumed session are counted up front so
            // the progress never restarts at zero.
            var queue = [];
            var done = 0;
            for (var i = 0; i < files.length; i++) {
                if (files[i].state === "done") {
                    done++;
                } else {
                    queue.push(files[i]);
                }
            }
            var total = files.length;
            var errors = 0;

            if (total === 0) {
                // An empty delta still gets a session, and in mirror mode it
                // can carry deletions. Route through the normal finish path so
                // they are applied before completing.
                btn.disabled = false;
                btn.textContent = "Sync Now";
                finishAndroidSync(profileName, sessionPath, delta, 0, 0, 0, function (ok) {
                    alert(ok ? "Already up to date" : "Could not apply deletions; a later sync will retry");
                });
                return;
            }

            showProgressTab(true);
            setProgressPulse(true);
            androidSyncStopRequested = false;
            androidSyncDirection = "pull";
            androidSyncCanStop = queue.length > 0;
            androidSyncCurrent = queue.length > 0 ? queue[0].path : "";
            setAndroidSyncActive(true);
            pausedSession = null;
            navigate("progress");
            updateSyncProgress(profileName, done, total, errors, androidSyncCurrent);

            var index = 0;

            function syncNext() {
                if (androidSyncStopRequested) {
                    pauseAndroidSync();
                    return;
                }
                if (index >= queue.length) {
                    androidSyncCanStop = false;
                    androidSyncCurrent = "";
                    updateSyncProgress(profileName, done, total, errors, "");
                    finishAndroidSync(profileName, sessionPath, delta, done, total, errors);
                    return;
                }

                var f = queue[index++];
                androidSyncCurrent = f.path;
                updateSyncProgress(profileName, done, total, errors, f.path);

                // A SHA256 on a pending file means the local copy is
                // size+mtime-ambiguous: hash it and confirm if it already
                // matches, otherwise (re)transfer.
                if (f.sha256) {
                    verifyAndroidFile(profileName, sessionPath, f.path, f.sha256, function (verified) {
                        if (androidSyncStopRequested) {
                            pauseAndroidSync();
                            return;
                        }
                        if (verified) {
                            done++;
                            syncNext();
                            return;
                        }
                        fetchAndroidFileWithRetry(profileName, sessionPath, f.path, function (ok) {
                            if (androidSyncStopRequested) {
                                pauseAndroidSync();
                                return;
                            }
                            if (ok) {
                                done++;
                            } else {
                                errors++;
                            }
                            syncNext();
                        });
                    });
                    return;
                }

                fetchAndroidFileWithRetry(profileName, sessionPath, f.path, function (ok) {
                    if (androidSyncStopRequested) {
                        pauseAndroidSync();
                        return;
                    }
                    if (ok) {
                        done++;
                    } else {
                        errors++;
                    }
                    syncNext();
                });
            }

            syncNext();
        }).catch(function (err) {
            setAndroidSyncActive(false);
            androidSyncCanStop = false;
            btn.disabled = false;
            btn.textContent = "Sync Now";
            if (activeScreen === "home") applyHomeActions();
            if (pausedSession && activeScreen === "progress") {
                renderPausedProgress();
            }
            alert(err.error || "Failed to start sync");
        });
    }

    // Confirms a size+mtime-ambiguous file without transferring it: hash the
    // local copy natively and ask the server to mark it done when it matches.
    function verifyAndroidFile(profileName, sessionPath, filePath, serverHash, onDone) {
        var bridgePath = profileName + "/" + filePath;

        window.AndroidBridge.hashFile(bridgePath, bridgeCallback(function (hashRes) {
            if (!hashRes.success) {
                onDone(false);
                return;
            }

            var localHash = null;
            try {
                localHash = JSON.parse(hashRes.data).hash;
            } catch (e) {
                onDone(false);
                return;
            }
            if (!localHash || localHash !== serverHash) {
                onDone(false);
                return;
            }

            fetch("/sync/confirm", {
                method: "POST",
                headers: {
                    "Content-Type": "application/json",
                    "X-Presync-Token": token
                },
                body: JSON.stringify({
                    session: sessionPath,
                    path: filePath,
                    sha256: localHash
                })
            }).then(function (res) {
                return res.json();
            }).then(function (result) {
                onDone(!!result.ok);
            }).catch(function () {
                onDone(false);
            });
        }));
    }

    // Retries a single file up to 3 times within one run, matching the PC
    // client's in-run retry behavior. Network failures keep the temp file, so
    // the retry resumes from its byte count.
    function fetchAndroidFileWithRetry(profileName, sessionPath, filePath, onDone) {
        var attempt = 0;

        function tryOnce() {
            attempt++;
            fetchAndroidFile(profileName, sessionPath, filePath, function (ok) {
                if (androidSyncStopRequested) {
                    onDone(false);
                    return;
                }
                if (ok || attempt >= 3) {
                    onDone(ok);
                    return;
                }
                setTimeout(tryOnce, 1000);
            });
        }

        tryOnce();
    }

    // Streams one file from the server in bounded chunks through the Android
    // bridge, verifies the SHA-256 natively, renames on confirmed success.
    function fetchAndroidFile(profileName, sessionPath, filePath, onDone) {
        var bridgePath = profileName + "/" + filePath;
        var CHUNK_BYTES = 512 * 1024;

        // A network failure keeps the temp file so the retry (or next run)
        // resumes from its byte count. A bad/complete-but-wrong temp is
        // discarded so the next attempt starts clean.
        function fail(discardTemp) {
            if (!discardTemp) {
                onDone(false);
                return;
            }
            window.AndroidBridge.abortFile(bridgePath, bridgeCallback(function () {
                onDone(false);
            }));
        }

        window.AndroidBridge.tempFileSize(bridgePath, bridgeCallback(function (sizeRes) {
            var offset = 0;
            if (sizeRes.success) {
                try {
                    offset = parseInt(sizeRes.data, 10) || 0;
                } catch (e) {
                    offset = 0;
                }
            }

            var url = "/sync/file?session=" + encodeURIComponent(sessionPath) +
                "&path=" + encodeURIComponent(filePath);
            if (offset > 0) url += "&offset=" + offset;

            var abortController = null;
            var fetchOpts = { headers: { "X-Presync-Token": token } };
            if (typeof AbortController !== "undefined") {
                abortController = new AbortController();
                androidSyncAbort = abortController;
                fetchOpts.signal = abortController.signal;
                // Per-file watchdog: a hung fetch must not stall the loop forever
                setTimeout(function () { abortController.abort(); }, 5 * 60 * 1000);
            }

            fetch(url, fetchOpts).then(function (res) {
                if (!res.ok) {
                    fail(true);
                    return;
                }
                if (!res.body || typeof res.body.getReader !== "function") {
                    fail(true);
                    return;
                }

                var reader = res.body.getReader();
                var failed = false;

                function pump() {
                    if (androidSyncStopRequested) {
                        try { reader.cancel(); } catch (e) { /* already closed */ }
                        onDone(false);
                        return;
                    }
                    return reader.read().then(function (result) {
                        if (failed) return;
                        if (result.done) {
                            reader.releaseLock();
                            window.AndroidBridge.finishFile(bridgePath, bridgeCallback(function (fin) {
                                if (!fin.success) {
                                    fail(true);
                                    return;
                                }
                                var hash = null;
                                try {
                                    hash = JSON.parse(fin.data).hash;
                                } catch (e) {
                                    fail(true);
                                    return;
                                }
                                var confirmBody = JSON.stringify({
                                    session: sessionPath,
                                    path: filePath,
                                    sha256: hash
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
                                        window.AndroidBridge.renameFile(bridgePath, bridgeCallback(function (ren) {
                                            onDone(ren.success);
                                        }));
                                    } else {
                                        fail(true);
                                    }
                                }).catch(function () {
                                    fail(true);
                                });
                            }));
                            return;
                        }

                        var b64 = toBase64(result.value);
                        return bridgeCallWithWatchdog("writeChunk", bridgePath, b64, function (wr) {
                            if (!wr.success) {
                                failed = true;
                                fail(true);
                                return;
                            }
                            pump();
                        });
                    }).catch(function () {
                        if (!failed) {
                            failed = true;
                            fail(false);
                        }
                    });
                }

                pump();
            }).catch(function () {
                fail(false);
            });
        }));
    }

    // Calls a bridge method with a timeout: if the callback never fires
    // (WebView death, process kill), the sync must not stall forever.
    function bridgeCallWithWatchdog(method, path, arg, onResult) {
        var called = false;
        var watchdog = setTimeout(function () {
            if (called) return;
            called = true;
            onResult({ success: false, data: "bridge call timed out" });
        }, 60 * 1000);

        window.AndroidBridge[method](path, arg, bridgeCallback(function (result) {
            if (called) return;
            called = true;
            clearTimeout(watchdog);
            onResult(result);
        }));
    }

    function toBase64(bytes) {
        var binary = "";
        var CHUNK = 0x8000;
        for (var i = 0; i < bytes.length; i += CHUNK) {
            binary += String.fromCharCode.apply(null, bytes.subarray(i, i + CHUNK));
        }
        return btoa(binary);
    }

    // Applies mirror-mode deletions, then completes the session.
    function finishAndroidSync(profileName, sessionPath, delta, done, total, errors, onDone) {
        // Never apply deletions or complete a session after a failed transfer:
        // the session stays resumable and Resume retries it.
        if (errors > 0) {
            androidSyncDone = done;
            androidSyncTotal = total;
            androidSyncErrors = errors;
            androidSyncCurrent = "";
            pauseAndroidSync();
            if (onDone) onDone(false);
            return;
        }

        var deletes = delta.to_delete || [];
        if (deletes.length === 0 || !isAndroid()) {
            completeAndroidSync(sessionPath, done, total, errors, onDone);
            return;
        }

        var i = 0;
        var deleteErrors = 0;

        function next() {
            if (i >= deletes.length) {
                if (deleteErrors > 0) {
                    androidSyncDone = done;
                    androidSyncTotal = total;
                    androidSyncErrors = errors + deleteErrors;
                    androidSyncCurrent = "";
                    pauseAndroidSync();
                    if (onDone) onDone(false);
                    return;
                }
                completeAndroidSync(sessionPath, done, total, errors, onDone);
                return;
            }
            var del = deletes[i++];
            window.AndroidBridge.deleteFile(profileName + "/" + del.path, bridgeCallback(function (result) {
                if (!result.success) deleteErrors++;
                next();
            }));
        }
        next();
    }

    function completeAndroidSync(sessionPath, done, total, errors, onDone) {
        fetch("/sync/complete", {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "X-Presync-Token": token
            },
            body: JSON.stringify({ session: sessionPath })
        }).then(function (res) {
            return res.json().then(function (data) {
                if (!res.ok) throw new Error(data.error || "complete failed");
                return data;
            });
        }).then(function () {
            setAndroidSyncActive(false);
            setProgressPulse(false);
            androidSyncCanStop = false;
            pausedSession = null;
            progressCompleted = {
                profile: androidSyncProfile,
                direction: androidSyncDirection,
                done: done,
                total: total,
                errors: errors
            };
            renderProgressView();
            refreshProgressTab();
            if (activeScreen === "home") applyHomeActions();
            if (onDone) onDone(true);
        }).catch(function () {
            // The server kept the session: offer Resume instead of losing it.
            androidSyncDone = done;
            androidSyncTotal = total;
            androidSyncErrors = errors;
            androidSyncCurrent = "";
            pauseAndroidSync();
            if (onDone) onDone(false);
        });
    }

    // --- Push (phone -> PC) ---

    function pushToDevice(profileName, btn) {
        if (androidSyncActive) {
            alert("A sync is already running.");
            return;
        }
        progressCompleted = null;
        androidSyncProfile = profileName;
        androidSyncDirection = "push";
        androidSyncDone = 0;
        androidSyncTotal = 0;
        androidSyncErrors = 0;
        androidSyncCurrent = "";
        setAndroidSyncActive(true);
        btn.disabled = true;
        btn.textContent = "Reading folder...";
        applyHomeActions();

        window.AndroidBridge.pushBuildManifest(profileName, bridgeCallback(function (manifestRes) {
            if (!manifestRes.success) {
                setAndroidSyncActive(false);
                btn.disabled = false;
                btn.textContent = "Send to PC";
                if (activeScreen === "home") applyHomeActions();
                if (pausedSession && activeScreen === "progress") {
                    renderPausedProgress();
                }
                alert(manifestRes.data || "Could not read the upload folder");
                return;
            }

            var manifest = { files: [] };
            try {
                manifest = JSON.parse(manifestRes.data);
            } catch (e) {
                manifest = { files: [] };
            }

            var deviceId = window.AndroidBridge.getDeviceId() || null;
            if (!deviceId) {
                api("GET", "/web/device-id").then(function (data) {
                    deviceId = data.device_id;
                    window.AndroidBridge.saveDeviceId(deviceId);
                    startAndroidPush(profileName, btn, deviceId, manifest);
                }).catch(function (err) {
                    setAndroidSyncActive(false);
                    btn.disabled = false;
                    btn.textContent = "Send to PC";
                    if (activeScreen === "home") applyHomeActions();
                    if (pausedSession && activeScreen === "progress") {
                        renderPausedProgress();
                    }
                    alert(err.error || "Failed to get device ID");
                });
                return;
            }

            startAndroidPush(profileName, btn, deviceId, manifest);
        }));
    }

    function startAndroidPush(profileName, btn, deviceId, sourceManifest) {
        btn.textContent = "Sending\u2026";

        fetch("/push", {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "X-Presync-Token": token
            },
            body: JSON.stringify({
                device_id: deviceId,
                profile: profileName,
                source_manifest: sourceManifest
            })
        }).then(function (res) {
            return res.json().then(function (data) {
                if (!res.ok) return Promise.reject(data);
                return data;
            });
        }).then(function (startResp) {
            var files = startResp.files || [];

            var queue = [];
            var done = 0;
            for (var i = 0; i < files.length; i++) {
                if (files[i].state === "done") {
                    done++;
                } else {
                    queue.push(files[i]);
                }
            }
            var total = files.length;
            var errors = 0;

            if (total === 0) {
                completeAndroidPush(startResp.session, btn, profileName, 0, 0, 0);
                alert("Already up to date");
                return;
            }

            showProgressTab(true);
            setProgressPulse(true);
            androidSyncStopRequested = false;
            androidSyncDirection = "push";
            androidSyncCanStop = queue.length > 0;
            androidSyncCurrent = queue.length > 0 ? queue[0].path : "";
            setAndroidSyncActive(true);
            pausedSession = null;
            navigate("progress");
            updateSyncProgress(profileName, done, total, errors, androidSyncCurrent);

            var index = 0;

            function next() {
                if (androidSyncStopRequested) {
                    pauseAndroidSync();
                    return;
                }
                if (index >= queue.length) {
                    androidSyncCanStop = false;
                    androidSyncCurrent = "";
                    updateSyncProgress(profileName, done, total, errors, "");
                    finishAndroidPush(profileName, startResp.session, btn, done, total, errors);
                    return;
                }

                var f = queue[index++];
                androidSyncCurrent = f.path;
                updateSyncProgress(profileName, done, total, errors, f.path);

                uploadPushFile(profileName, startResp.session, f, function (ok) {
                    if (androidSyncStopRequested) {
                        pauseAndroidSync();
                        return;
                    }
                    if (ok) {
                        done++;
                    } else {
                        errors++;
                    }
                    next();
                });
            }

            next();
        }).catch(function (err) {
            setAndroidSyncActive(false);
            androidSyncCanStop = false;
            btn.disabled = false;
            btn.textContent = "Send to PC";
            if (activeScreen === "home") applyHomeActions();
            if (pausedSession && activeScreen === "progress") {
                renderPausedProgress();
            }
            alert(err.error || "Failed to start upload");
        });
    }

    function uploadPushFile(profileName, sessionPath, file, onDone) {
        var attempt = 0;

        function tryFrom(offset) {
            if (androidSyncStopRequested) {
                onDone(false);
                return;
            }
            attempt++;

            uploadPushChunks(profileName, sessionPath, file.path, offset, function (ok) {
                if (androidSyncStopRequested) {
                    onDone(false);
                    return;
                }
                if (!ok) {
                    retryOrFail();
                    return;
                }

                confirmPushFile(profileName, sessionPath, file.path, function (confirmed) {
                    if (androidSyncStopRequested) {
                        onDone(false);
                        return;
                    }
                    if (confirmed) {
                        onDone(true);
                        return;
                    }
                    retryOrFail();
                });
            });
        }

        function retryOrFail() {
            if (androidSyncStopRequested) {
                onDone(false);
                return;
            }
            if (attempt >= 3) {
                reportPushError(sessionPath, file.path);
                onDone(false);
                return;
            }
            fetchPushOffset(sessionPath, file.path, function (offset) {
                setTimeout(function () {
                    tryFrom(offset);
                }, 1000);
            });
        }

        tryFrom(file.offset || 0);
    }

    function uploadPushChunks(profileName, sessionPath, filePath, offset, onDone) {
        var CHUNK_BYTES = 256 * 1024;

        function readNext(at) {
            if (androidSyncStopRequested) {
                onDone(false);
                return;
            }
            window.AndroidBridge.pushReadChunk(profileName, filePath, at, CHUNK_BYTES, bridgeCallback(function (res) {
                if (androidSyncStopRequested) {
                    onDone(false);
                    return;
                }
                if (!res.success) {
                    onDone(false);
                    return;
                }

                var parsed;
                try {
                    parsed = JSON.parse(res.data);
                } catch (e) {
                    onDone(false);
                    return;
                }

                var bytes;
                try {
                    bytes = base64ToBytes(parsed.data || "");
                } catch (e) {
                    onDone(false);
                    return;
                }

                if (bytes.length === 0) {
                    // An exact multiple-of-chunk file ends with a zero-length
                    // read; that is a successful completion.
                    onDone(!!parsed.eof);
                    return;
                }

                var query = "?session=" + encodeURIComponent(sessionPath) +
                    "&path=" + encodeURIComponent(filePath) +
                    "&offset=" + at;

                fetch("/push/file" + query, {
                    method: "POST",
                    headers: { "X-Presync-Token": token },
                    body: bytes
                }).then(function (res) {
                    return res.json().then(function (body) {
                        if (!res.ok) {
                            if (res.status === 409 && body && typeof body.offset === "number") {
                                return body;
                            }
                            return Promise.reject(body);
                        }
                        return body;
                    });
                }).then(function (body) {
                    var next = body && typeof body.offset === "number" ? body.offset : at + bytes.length;
                    if (parsed.eof) {
                        onDone(true);
                    } else {
                        readNext(next);
                    }
                }).catch(function () {
                    onDone(false);
                });
            }));
        }

        readNext(offset);
    }

    function confirmPushFile(profileName, sessionPath, filePath, onDone) {
        window.AndroidBridge.pushHashFile(profileName, filePath, bridgeCallback(function (hashRes) {
            if (!hashRes.success) {
                onDone(false);
                return;
            }

            var hash = null;
            try {
                hash = JSON.parse(hashRes.data).hash;
            } catch (e) {
                onDone(false);
                return;
            }
            if (!hash) {
                onDone(false);
                return;
            }

            fetch("/push/confirm", {
                method: "POST",
                headers: {
                    "Content-Type": "application/json",
                    "X-Presync-Token": token
                },
                body: JSON.stringify({
                    session: sessionPath,
                    path: filePath,
                    sha256: hash
                })
            }).then(function (res) {
                return res.json();
            }).then(function (result) {
                onDone(!!result.ok);
            }).catch(function () {
                onDone(false);
            });
        }));
    }

    function fetchPushOffset(sessionPath, filePath, onDone) {
        fetch("/push/status?session=" + encodeURIComponent(sessionPath), {
            headers: { "X-Presync-Token": token }
        }).then(function (res) {
            return res.json();
        }).then(function (data) {
            var files = data.files || [];
            for (var i = 0; i < files.length; i++) {
                if (files[i].path === filePath) {
                    onDone(files[i].offset || 0);
                    return;
                }
            }
            onDone(0);
        }).catch(function () {
            onDone(0);
        });
    }

    function reportPushError(sessionPath, filePath) {
        fetch("/push/error", {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "X-Presync-Token": token
            },
            body: JSON.stringify({ session: sessionPath, path: filePath })
        }).catch(function () {
            // Best effort — the session remains resumable either way.
        });
    }

    function finishAndroidPush(profileName, sessionPath, btn, done, total, errors) {
        if (errors > 0) {
            btn.disabled = false;
            btn.textContent = "Send to PC";
            androidSyncDone = done;
            androidSyncTotal = total;
            androidSyncErrors = errors;
            androidSyncCurrent = "";
            pauseAndroidSync();
            return;
        }
        completeAndroidPush(sessionPath, btn, profileName, done, total, errors);
    }

    function completeAndroidPush(sessionPath, btn, profileName, done, total, errors) {
        btn.disabled = false;
        btn.textContent = "Send to PC";

        fetch("/push/complete", {
            method: "POST",
            headers: {
                "Content-Type": "application/json",
                "X-Presync-Token": token
            },
            body: JSON.stringify({ session: sessionPath })
        }).then(function (res) {
            return res.json().then(function (data) {
                if (!res.ok) throw new Error(data.error || "complete failed");
                return data;
            });
        }).then(function () {
            setAndroidSyncActive(false);
            setProgressPulse(false);
            androidSyncCanStop = false;
            pausedSession = null;
            progressCompleted = {
                profile: profileName,
                direction: androidSyncDirection,
                done: done,
                total: total,
                errors: errors
            };
            renderProgressView();
            refreshProgressTab();
            if (activeScreen === "home") applyHomeActions();
        }).catch(function () {
            // The server kept the session: Resume can finish the upload.
            androidSyncDone = done;
            androidSyncTotal = total;
            androidSyncErrors = errors;
            androidSyncCurrent = "";
            pauseAndroidSync();
        });
    }

    function base64ToBytes(base64) {
        var binary = atob(base64);
        var bytes = new Uint8Array(binary.length);
        for (var i = 0; i < binary.length; i++) {
            bytes[i] = binary.charCodeAt(i);
        }
        return bytes;
    }

    function updateSyncProgress(profileName, done, total, errors, current) {
        if (profileName) androidSyncProfile = profileName;
        androidSyncDone = done;
        androidSyncTotal = total;
        androidSyncErrors = errors;
        androidSyncCurrent = current || "";

        renderProgressView();
    }

    function renderProgress(el) {
        progressSessions = [];
        progressCompleted = null;
        el.innerHTML = '<h1>Sync Progress</h1><div class="loading">Loading...</div>';
        if (androidSyncActive || pausedSession) {
            renderProgressView();
        }

        refreshProgressSessions();
        pollInterval = setInterval(refreshProgressSessions, 2000);
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
                var isPush = p.direction === "push";

                html +=
                    '<div class="card">' +
                    '<div class="card-title">' + esc(p.name) + "</div>";

                if (isPush) {
                    html += '<div class="setting-row"><span class="setting-label">Direction</span><span class="mode-badge">push</span></div>';
                    if (isAndroid()) {
                        var pushSource = window.AndroidBridge.getPushSourceDisplayPath(p.name) || "";
                        if (pushSource) {
                            html += '<div class="setting-row"><span class="setting-label">Upload folder</span><span class="setting-value">' + esc(pushSource) + "</span></div>";
                        } else {
                            html += '<div class="setting-row"><span class="setting-label">Upload folder</span><span class="setting-value" style="color:var(--status-warning)">No folder selected</span></div>';
                        }
                    } else {
                        html += '<div class="setting-row"><span class="setting-label">Receive folder</span><span class="setting-value">' + esc(p.destination || "") + "</span></div>";
                    }
                } else {
                    html += '<div class="setting-row"><span class="setting-label">Source</span><span class="setting-value">' + esc(p.source) + "</span></div>";
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
                                confirmBtn.textContent = "Pick a folder\u2026";
                                pinDisplay.textContent = "Paired!";
                                pinDisplay.style.color = "var(--status-success)";

                                try {
                                    window.AndroidBridge.saveToken(result.token);
                                    window.AndroidBridge.saveServerUrl(location.origin);
                                    if (result.device_id) {
                                        window.AndroidBridge.saveDeviceId(result.device_id);
                                    }

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
                                } catch (e) {
                                    pinDisplay.textContent = "Paired, but setup failed";
                                    pinDisplay.style.color = "var(--status-error)";
                                    errorEl.textContent = "Could not finish setup on this device: " +
                                        (e && e.message ? e.message : String(e));
                                }
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
            refreshProgressTab();
        } else {
            api("GET", "/web/token").then(function (data) {
                token = data.token;
                window.addEventListener("hashchange", onHashChange);
                render(getHash());
                refreshProgressTab();
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
