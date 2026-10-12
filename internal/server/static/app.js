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
  const chip = $("vpnState");
  const status = vpn?.state || "disconnected";
  chip.textContent = ({ disconnected: "Не подключён", connecting: "Подключение…", connected: "Подключён", stopping: "Отключение…" })[status] || status;
  chip.className = `state-chip ${status}`;
  $("currentProfile").textContent = status === "disconnected" ? "Нет подключения" : (vpn?.profile_name || "VPN");
  $("currentPid").textContent = vpn?.pid ? String(vpn.pid) : "-";
  $("currentSince").textContent = status !== "disconnected" && vpn?.started_at ? formatTime(vpn.started_at) : "—";
  $("disconnectBtn").disabled = state.busy || status === "disconnected" || status === "stopping";
  $("connectionError").textContent = vpn?.last_error ? `Соединение завершилось с ошибкой: ${vpn.last_error}. Подробности в журнале.` : "";
  $("connectionError").hidden = !vpn?.last_error;
  if (state.profiles.length > 0) {
    renderProfiles();
  }
}

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
  if (state.selectedId && !state.profiles.some((profile) => profile.id === state.selectedId)) {
    state.selectedId = "";
  }
  renderProfiles();
}

async function loadSettings() {
  state.settings = await api("/api/settings");
  $("openconnectPath").value = state.settings.openconnect_path || "openconnect";
}

async function loadLogs() {
  const data = await api("/api/logs");
  const lines = (data.logs || []).map((entry) => {
    return `[${formatTime(entry.time)}] ${entry.message}`;
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
  const key = JSON.stringify([state.profiles, state.selectedId, state.status?.vpn?.state, state.status?.vpn?.profile_id, query, state.busy]);
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
    const vpn = state.status?.vpn || {};
    const isActive = vpn.profile_id === profile.id && vpn.state && vpn.state !== "disconnected";
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

    const actions = document.createElement("div");
    actions.className = "profile-actions";

    const connect = document.createElement("button");
    connect.type = "button";
    connect.textContent = isActive ? "Отключить" : "Подключить";
    connect.disabled = state.busy || vpn.state === "stopping";
    connect.className = isActive ? "secondary" : "";
    connect.addEventListener("click", () => {
      if (isActive) {
        runAction(disconnectVpn);
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

async function disconnectVpn() {
  await api("/api/disconnect", { method: "POST", body: "{}" });
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
$("disconnectBtn").addEventListener("click", () => runAction(disconnectVpn));
$("lockBtn").addEventListener("click", () => runAction(async () => {
  await api("/api/lock", { method: "POST", body: "{}" });
  renderAuth("unlock");
}));
$("saveSettingsBtn").addEventListener("click", () => runAction(async () => {
  const payload = { openconnect_path: $("openconnectPath").value.trim() || "openconnect" };
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
