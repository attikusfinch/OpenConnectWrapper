const state = {
  initialized: false,
  unlocked: false,
  profiles: [],
  settings: { openconnect_path: "openconnect" },
  status: null,
  selectedId: "",
  authMode: "unlock",
  profilesKey: "",
  busy: false,
  selectedProfiles: new Set(),
  connectionsKey: "",
};

const $ = (id) => document.getElementById(id);

const authView = $("authView");
const appView = $("appView");
const authForm = $("authForm");
const pinInput = $("pinInput");
const authSubmit = $("authSubmit");
const authSubtitle = $("authSubtitle");
const authError = $("authError");
const profilesList = $("profilesList");
const profileForm = $("profileForm");
const editorOverlay = $("editorOverlay");
const pathWarning = $("pathWarning");
const toast = $("toast");

async function api(path, options = {}) {
  const response = await fetch(path, {
    credentials: "same-origin",
    headers: { "Content-Type": "application/json", ...(options.headers || {}) },
    ...options,
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok) {
    const error = new Error(data.error || `HTTP ${response.status}`);
    error.status = response.status;
    error.data = data;
    throw error;
  }
  return data;
}

function showToast(message) {
  toast.textContent = message;
  toast.hidden = false;
  window.clearTimeout(showToast.timer);
  showToast.timer = window.setTimeout(() => {
    toast.hidden = true;
  }, 3600);
}

async function refreshStatus() {
  const data = await api("/api/status");
  state.initialized = data.initialized;
  state.unlocked = data.unlocked;
  state.status = data;

  if (!state.initialized) {
    renderAuth("setup");
    return;
  }
  if (!state.unlocked) {
    renderAuth("unlock");
    return;
  }

  const firstRender = appView.hidden;
  renderAppShell();
  updateVpnStatus(data.vpn);
  updatePathWarning(data);
  $("agentState").textContent = data.agent_enabled ? "Доступен локально" : "Недоступен";
  $("agentState").className = `state-chip ${data.agent_enabled ? "connected" : ""}`;
  $("agentActivity").textContent = data.agent_last_action ? `${data.agent_last_action} · ${formatTime(data.agent_last_at)}` : "Агент ещё не менял подключение в этой сессии.";
  if (firstRender) {
    await Promise.all([loadProfiles(), loadSettings(), loadLogs()]);
  }
}

function renderAuth(mode) {
  const entering = authView.hidden || state.authMode !== mode;
  state.authMode = mode;
  state.unlocked = false;
  closeEditor();
  state.profiles = [];
  state.profilesKey = "";
  profilesList.replaceChildren();
  profileForm.reset();
  $("logsOutput").textContent = "";
  appView.hidden = true;
  authView.hidden = false;
  authSubmit.textContent = mode === "setup" ? "Создать" : "Открыть";
  authSubtitle.textContent = mode === "setup" ? "Создайте PIN из 4 цифр для защиты профилей" : "Введите PIN из 4 цифр, чтобы открыть профили";
  if (entering) {
    pinInput.value = "";
    pinInput.focus();
  }
}

function renderAppShell() {
  authView.hidden = true;
  appView.hidden = false;
  $("vaultPath").textContent = "Локальный менеджер VPN";
  $("vaultPath").title = state.status?.vault_path || "";
}

function updateVpnStatus(vpn) {
  const connections = state.status?.connections || (vpn?.profile_id ? [vpn] : []);
  const active = connections.filter(c => c.state !== "disconnected");
  const chip = $("vpnState");
  const status = active.some(c => c.state === "stopping") ? "stopping" : active.some(c => c.state === "connecting") ? "connecting" : active.length ? "connected" : "disconnected";
  chip.textContent = active.length ? `Активных: ${active.length}${status === "connecting" ? " · подключение…" : status === "stopping" ? " · отключение…" : ""}` : "Не подключён";
  chip.className = `state-chip ${status}`;
  $("disconnectBtn").disabled = state.busy || status === "disconnected" || status === "stopping";
  const errors = connections.filter(c => c.last_error).map(c => `${c.profile_name}: ${c.last_error}`);
  $("connectionError").textContent = errors.join("\n");
  $("connectionError").hidden = !errors.length;
  const key = JSON.stringify(connections);
  if (key !== state.connectionsKey) {
    state.connectionsKey = key;
    const list = $("connectionsList");
    list.replaceChildren();
    if (!active.length) { const p = document.createElement("p"); p.className = "muted"; p.textContent = "Нет активных подключений. Выберите профиль слева."; list.append(p); }
    for (const c of active) {
      const row = document.createElement("article"); row.className = "connection-row";
      const heading = document.createElement("div"); heading.className = "pane-head";
      const name = document.createElement("strong"); name.textContent = c.profile_name;
      const badge = document.createElement("span"); badge.className = `state-chip ${c.state}`; badge.textContent = stateLabel(c.state);
      heading.append(name, badge); row.append(heading);
      const detail = document.createElement("p"); detail.className = "muted";
      detail.textContent = `${c.multi ? (c.network?.full ? "Интернет и рабочие сети" : "Только рабочие сети") : "Один VPN"} · ${formatTime(c.started_at)} · PID ${c.pid}`;
      row.append(detail);
      if (c.network) {
        const routes = document.createElement("p"); routes.className = "network-detail";
        routes.textContent = `Маршруты: ${(c.network.routes || []).join(", ") || "—"}`; row.append(routes);
        const dns = document.createElement("p"); dns.className = "network-detail";
        dns.textContent = `DNS: ${(c.network.dns_servers || []).join(", ") || "—"} · Домены: ${(c.network.dns_domains || []).join(", ") || "не заданы"}`; row.append(dns);
        if (c.network.warning) { const note=document.createElement("p"); note.className="muted"; note.textContent=c.network.warning; row.append(note); }
      }
      const note = document.createElement("p"); note.className = "muted";
      note.textContent = c.state === "connected" ? (c.multi ? "Туннель настроен. Доступность рабочих ресурсов ещё нужно проверить." : "Сервер выдал адрес. Доступность рабочих ресурсов ещё нужно проверить.") : "Ожидаем настройки туннеля…"; row.append(note);
      list.append(row);
    }
  }
  if (state.profiles.length > 0) {
    renderProfiles();
  }
}

function stateLabel(value) { return ({ disconnected: "Не подключён", connecting: "Подключение…", connected: "Настроен", stopping: "Отключение…" })[value] || value; }

function updatePathWarning(status) {
  const configuredPath = status.openconnect_path || "openconnect";
  const effectivePath = status.effective_openconnect_path || configuredPath;
  const lower = `${configuredPath} ${effectivePath}`.toLowerCase();
  let message = "";
  if (lower.includes("openconnect-gui")) {
    message = "Указан openconnect-gui.exe. Для автоподстановки пароля нужен CLI-бинарник openconnect.exe.";
  } else if (!status.openconnect_found && status.known_gui_path) {
    message = `Найден OpenConnect-GUI: ${status.known_gui_path}. В этой установке нет openconnect.exe, поэтому добавь CLI-бинарник и укажи путь к нему.`;
  } else if (!status.openconnect_found) {
    message = "openconnect.exe не найден. Укажи полный путь к CLI-бинарнику в настройках.";
  }
  pathWarning.textContent = message;
  pathWarning.hidden = message === "";
}

async function loadProfiles() {
  const data = await api("/api/profiles");
  if (!state.unlocked) return;
  state.profiles = data.profiles || [];
  state.selectedProfiles = new Set([...state.selectedProfiles].filter(id => state.profiles.some(p => p.id === id)));
  if (state.selectedId && !state.profiles.some((profile) => profile.id === state.selectedId)) {
    state.selectedId = "";
  }
  renderProfiles();
  updateModeControls();
}

async function loadSettings() {
  state.settings = await api("/api/settings");
  $("openconnectPath").value = state.settings.openconnect_path || "openconnect";
  $("connectionMode").value = state.settings.multi_mode ? "multi" : "single";
  updateModeControls();
  renderProfiles();
}

async function loadLogs() {
  const data = await api("/api/logs");
  const lines = (data.logs || []).filter(entry => !$("logProfile").value || entry.profile_id === $("logProfile").value).map((entry) => {
    return `[${formatTime(entry.time)}] ${entry.profile_name ? `[${entry.profile_name}] ` : ""}${entry.message}`;
  });
  if (!state.unlocked) return;
  const output = $("logsOutput");
  const atBottom = output.scrollHeight - output.scrollTop - output.clientHeight < 40;
  const content = lines.length ? lines.join("\n") : "Здесь появятся события подключения.\nВыберите профиль и нажмите «Подключить».";
  if (output.textContent !== content) {
    output.textContent = content;
    if (atBottom) output.scrollTop = output.scrollHeight;
  }
}

function renderProfiles() {
  const query = $("profileSearch").value.trim().toLowerCase();
  const key = JSON.stringify([state.profiles, state.selectedId, state.status?.connections, [...state.selectedProfiles], state.settings.multi_mode, query, state.busy]);
  if (key === state.profilesKey) return;
  state.profilesKey = key;
  profilesList.replaceChildren();
  if (state.profiles.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "Добавьте первый VPN-профиль: адрес сервера, логин и пароль.";
    const add = document.createElement("button");
    add.type = "button";
    add.textContent = "Добавить профиль";
    add.addEventListener("click", newProfile);
    empty.append(add);
    profilesList.append(empty);
    return;
  }

  const filtered = state.profiles.filter((p) => `${p.name} ${p.server}`.toLowerCase().includes(query));
  if (!filtered.length) {
    const empty = document.createElement("p");
    empty.className = "empty";
    empty.textContent = "Ничего не найдено. Попробуйте другой запрос.";
    profilesList.append(empty);
  }
  for (const profile of filtered) {
    const vpn = state.status?.connections?.find(c => c.profile_id === profile.id) || {};
    const isActive = vpn.state && vpn.state !== "disconnected";
    const item = document.createElement("article");
    item.className = `profile-item ${isActive ? "active" : ""}`;

    const avatar = document.createElement("span");
    avatar.className = "profile-avatar";
    avatar.textContent = avatarLabel(profile);

    const content = document.createElement("div");
    content.className = "profile-content";

    const main = document.createElement("div");
    main.className = "profile-main";
    const title = document.createElement("strong");
    title.textContent = profile.name;
    const server = document.createElement("span");
    server.className = "muted";
    server.textContent = profile.server;
    main.append(title, server);
    const statusText = document.createElement("span"); statusText.className="muted"; statusText.textContent=stateLabel(vpn.state || "disconnected"); main.append(statusText);
    if (state.settings.multi_mode) {
      const label=document.createElement("label"); label.className="check-row profile-selection";
      const check=document.createElement("input"); check.type="checkbox"; check.checked=state.selectedProfiles.has(profile.id); check.disabled=state.busy;
      const caption=document.createElement("span"); caption.textContent="Включить в выбор";
      check.addEventListener("change",()=>{ if(check.checked) state.selectedProfiles.add(profile.id); else state.selectedProfiles.delete(profile.id); updateSelectionControls(); });
      label.append(check,caption); main.append(label);
    }

    const actions = document.createElement("div");
    actions.className = "profile-actions";

    const connect = document.createElement("button");
    connect.type = "button";
    connect.textContent = isActive ? "Отключить" : "Подключить";
    connect.disabled = state.busy || vpn.state === "stopping";
    connect.className = isActive ? "secondary" : "";
    connect.addEventListener("click", () => {
      if (isActive) {
        runAction(() => disconnectVpn(profile.id));
        return;
      }
      runAction(() => connectProfile(profile.id));
    });

    const menuWrap = document.createElement("div");
    menuWrap.className = "profile-menu";

    const menuButton = document.createElement("button");
    menuButton.type = "button";
    menuButton.className = "secondary icon-button";
    menuButton.textContent = "...";
    menuButton.title = "Действия с профилем";
    menuButton.setAttribute("aria-label", `Действия: ${profile.name}`);
    menuButton.setAttribute("aria-expanded", "false");
    menuButton.addEventListener("click", (event) => {
      event.stopPropagation();
      toggleProfileMenu(menuWrap);
    });

    const menuPanel = document.createElement("div");
    menuPanel.className = "profile-menu-panel";

    const edit = document.createElement("button");
    edit.type = "button";
    edit.className = "ghost";
    edit.textContent = "Изменить";
    edit.addEventListener("click", () => {
      closeProfileMenus();
      editProfile(profile);
    });

    const del = document.createElement("button");
    del.type = "button";
    del.className = "danger";
    del.textContent = "Удалить";
    del.addEventListener("click", () => {
      closeProfileMenus();
      runAction(() => deleteProfile(profile));
    });

    menuPanel.append(edit, del);
    menuWrap.append(menuButton, menuPanel);
    actions.append(connect, menuWrap);
    content.append(main, actions);
    item.append(avatar, content);
    profilesList.append(item);
  }
  updateSelectionControls();
}

function updateSelectionControls() {
  const count=state.selectedProfiles.size;
  $("connectSelectedBtn").textContent=count ? `Подключить выбранные (${count})` : "Подключить выбранные";
  $("connectSelectedBtn").disabled=state.busy || count===0;
  $("selectAllProfiles").checked=count>0 && count===state.profiles.length;
  $("selectAllProfiles").indeterminate=count>0 && count<state.profiles.length;
}

function updateModeControls() {
  const multi=$("connectionMode").value === "multi";
  $("internetProfileField").hidden=!multi;
  $("selectionActions").hidden=!state.settings.multi_mode;
  const desired=state.settings.internet_profile || "";
  for (const id of ["internetProfile","logProfile"]) {
    const select=$(id), previous=id==="internetProfile" ? desired : select.value;
    const options=JSON.stringify(state.profiles.map(p=>[p.id,p.name]));
    if(select.dataset.options!==options) {
      select.dataset.options=options;
      select.replaceChildren(new Option(id==="internetProfile" ? "Обычное подключение" : "Все профили",""));
      for(const p of state.profiles) select.add(new Option(p.name,p.id));
      select.value=previous;
    }
  }
  $("internetProfile").value=desired;
  $("modeDescription").textContent=multi ? "Мульти: выбранный VPN обслуживает интернет, остальные — свои рабочие сети. Без VPN для интернета используется обычное подключение. IPv4; IPv6 остаётся через провайдера. Для смены режима сначала отключите все VPN." : "Один VPN: подключение другого профиля заменяет текущее.";
}

function toggleProfileMenu(menu) {
  const wasOpen = menu.classList.contains("open");
  closeProfileMenus();
  if (!wasOpen) {
    menu.classList.add("open");
    menu.querySelector("button").setAttribute("aria-expanded", "true");
  }
}

function closeProfileMenus() {
  document.querySelectorAll(".profile-menu.open").forEach((menu) => {
    menu.classList.remove("open");
    menu.querySelector("button").setAttribute("aria-expanded", "false");
  });
}

function avatarLabel(profile) {
  const source = (profile.name || profile.server || "VPN").trim();
  return source.slice(0, 2).toUpperCase();
}

function editProfile(profile) {
  state.selectedId = profile.id;
  $("editorTitle").textContent = `Профиль: ${profile.name}`;
  $("profileId").value = profile.id;
  $("profileName").value = profile.name || "";
  $("profileServer").value = profile.server || "";
  $("profileUsername").value = profile.username || "";
  $("profilePassword").value = "";
  $("profilePassword").placeholder = profile.has_password ? "сохранен, оставь пустым" : "";
  $("profileAuthGroup").value = profile.auth_group || "";
  $("profileProtocol").value = profile.protocol || "";
  $("profileUserAgent").value = profile.user_agent || "";
  $("profileServerCert").value = profile.server_cert || "";
  $("profileNoCertCheck").checked = Boolean(profile.no_cert_check);
  $("profileExtraArgs").value = (profile.extra_args || []).join("\n");
  $("profileRoutes").value = (profile.routes || []).join("\n");
  $("profileDNSDomains").value = (profile.dns_domains || []).join("\n");
  openEditor();
  renderProfiles();
}

function newProfile() {
  state.selectedId = "";
  $("editorTitle").textContent = "Новый профиль";
  profileForm.reset();
  $("profileId").value = "";
  $("profilePassword").placeholder = "";
  openEditor();
  renderProfiles();
}

let editorTrigger;
function openEditor() {
  editorTrigger = document.activeElement;
  $("appView").querySelector(".topbar").inert = true;
  document.querySelector(".profiles-pane").inert = true;
  editorOverlay.hidden = false;
  document.body.classList.add("modal-open");
  window.setTimeout(() => $("profileName").focus(), 0);
}

function closeEditor() {
  const wasOpen = !editorOverlay.hidden;
  $("appView").querySelector(".topbar").inert = false;
  document.querySelector(".profiles-pane").inert = false;
  editorOverlay.hidden = true;
  document.body.classList.remove("modal-open");
  $("profilePassword").value = "";
  if (wasOpen) (editorTrigger?.isConnected ? editorTrigger : $("newProfileBtn")).focus();
}

async function saveProfile() {
  if (!profileForm.reportValidity()) return null;
  const payload = {
    id: $("profileId").value.trim(),
    name: $("profileName").value.trim(),
    server: $("profileServer").value.trim(),
    username: $("profileUsername").value.trim(),
    password: $("profilePassword").value,
    auth_group: $("profileAuthGroup").value.trim(),
    protocol: $("profileProtocol").value,
    user_agent: $("profileUserAgent").value.trim(),
    server_cert: $("profileServerCert").value.trim(),
    no_cert_check: $("profileNoCertCheck").checked,
    extra_args: $("profileExtraArgs").value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean),
    routes: $("profileRoutes").value.split(/\r?\n/).map(line=>line.trim()).filter(Boolean),
    dns_domains: $("profileDNSDomains").value.split(/\r?\n/).map(line=>line.trim()).filter(Boolean),
  };
  const data = await api("/api/profiles", {
    method: "POST",
    body: JSON.stringify(payload),
  });
  state.selectedId = data.profile.id;
  showToast("Профиль сохранен");
  await loadProfiles();
  closeEditor();
  return data.profile;
}

async function connectProfile(id) {
  try {
    await api(`/api/connect/${encodeURIComponent(id)}`, { method: "POST", body: "{}" });
    showToast("OpenConnect запущен");
    await refreshStatus();
    await loadLogs();
  } catch (error) {
    await loadLogs().catch(() => {});
    throw error;
  }
}

async function disconnectVpn(id) {
  await api(id ? `/api/disconnect/${encodeURIComponent(id)}` : "/api/disconnect", { method: "POST", body: "{}" });
  showToast("OpenConnect остановлен");
  await refreshStatus();
  await loadLogs();
}

async function deleteProfile(profile) {
  const ok = window.confirm(`Удалить профиль "${profile.name}"? Это действие нельзя отменить.`);
  if (!ok) {
    return;
  }
  await api(`/api/profiles/${encodeURIComponent(profile.id)}`, { method: "DELETE" });
  if (state.selectedId === profile.id) {
    closeEditor();
    state.selectedId = "";
  }
  await loadProfiles();
  showToast("Профиль удален");
}

function formatTime(value) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "-";
  }
  return date.toLocaleString("ru-RU", {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    day: "2-digit",
    month: "2-digit",
  });
}

async function runAction(action) {
  if (state.busy) return;
  state.busy = true;
  const buttons = [...document.querySelectorAll("button")];
  const disabled = buttons.map((button) => button.disabled);
  buttons.forEach((button) => { button.disabled = true; });
  try { await action(); }
  catch (error) {
    showToast(error.message);
    if (error.status === 401) renderAuth("unlock");
  } finally {
    state.busy = false;
    buttons.forEach((button, i) => { button.disabled = disabled[i]; });
    if (state.unlocked) updateVpnStatus(state.status?.vpn);
  }
}

authForm.addEventListener("submit", (event) => {
  event.preventDefault();
  runAction(async () => {
    authError.textContent = "";
    const pin = pinInput.value.trim();
    if (!/^[0-9]{4}$/.test(pin)) { authError.textContent = "Введите ровно 4 цифры."; return; }
    try {
      await api(state.authMode === "setup" ? "/api/setup" : "/api/unlock", { method: "POST", body: JSON.stringify({ pin }) });
      pinInput.value = "";
      await refreshStatus();
    } catch (error) {
      if (error.status === 409) renderAuth("unlock");
      authError.textContent = error.status === 401 ? "Неверный PIN. Попробуйте ещё раз." : error.status === 429 ? "Слишком много попыток. Подождите 30 секунд." : error.message;
    }
  });
});
profileForm.addEventListener("submit", (event) => { event.preventDefault(); runAction(saveProfile); });
$("profileSearch").addEventListener("input", renderProfiles);
$("newProfileBtn").addEventListener("click", newProfile);
$("clearEditorBtn").addEventListener("click", closeEditor);
$("refreshLogsBtn").addEventListener("click", () => runAction(loadLogs));
$("logProfile").addEventListener("change", () => runAction(loadLogs));
$("connectionMode").addEventListener("change", updateModeControls);
$("saveModeBtn").addEventListener("click", () => runAction(async () => {
  $("modeError").hidden=true;
  try {
    state.settings=await api("/api/settings",{method:"POST",body:JSON.stringify({...state.settings,multi_mode:$("connectionMode").value==="multi",internet_profile:$("internetProfile").value})});
    renderProfiles(); updateModeControls(); showToast("Режим сохранён");
  } catch(error) { $("modeError").textContent=error.message; $("modeError").hidden=false; throw error; }
}));
$("selectAllProfiles").addEventListener("change",()=>{ state.selectedProfiles=new Set($("selectAllProfiles").checked ? state.profiles.map(p=>p.id) : []); renderProfiles(); });
$("connectSelectedBtn").addEventListener("click",()=>runAction(async()=>{
  const result=await api("/api/connect-selected",{method:"POST",body:JSON.stringify({profiles:[...state.selectedProfiles]})});
  await refreshStatus(); await loadLogs();
  showToast(result.ok ? "Выбранные подключения запущены" : Object.entries(result.errors).map(([id,message])=>`${state.profiles.find(p=>p.id===id)?.name || id}: ${message}`).join("; "));
}));
$("connectEditorBtn").addEventListener("click", () => runAction(async () => {
  const saved = await saveProfile();
  if (saved?.id) await connectProfile(saved.id);
}));
editorOverlay.addEventListener("click", (event) => {
  if (event.target === editorOverlay && !state.busy) closeEditor();
});
window.addEventListener("keydown", (event) => {
  if (event.key === "Escape") {
    if (!state.busy && !editorOverlay.hidden) closeEditor();
    closeProfileMenus();
  }
  if (event.key === "Tab" && !editorOverlay.hidden) {
    const elements = [...editorOverlay.querySelectorAll('button:not(:disabled), input:not([type="hidden"]), select, textarea, summary')].filter(el => el.getClientRects().length);
    const first = elements[0], last = elements[elements.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }
});
document.addEventListener("click", closeProfileMenus);
$("disconnectBtn").addEventListener("click", () => runAction(() => disconnectVpn()));
$("lockBtn").addEventListener("click", () => runAction(async () => {
  await api("/api/lock", { method: "POST", body: "{}" });
  renderAuth("unlock");
}));
$("saveSettingsBtn").addEventListener("click", () => runAction(async () => {
  const payload = { ...state.settings, openconnect_path: $("openconnectPath").value.trim() || "openconnect" };
  state.settings = await api("/api/settings", { method: "POST", body: JSON.stringify(payload) });
  showToast("Настройки сохранены");
  await refreshStatus();
}));
async function poll() {
  try {
    if (!state.busy) {
      await refreshStatus();
      if (state.unlocked) await loadLogs();
    }
  } catch (error) {
    if (authView.hidden) showToast("Нет связи с приложением. Повторяем попытку…");
    else { authSubtitle.textContent = "Ожидание приложения…"; authError.textContent = error.message; }
  } finally { window.setTimeout(poll, 2500); }
}
poll();
