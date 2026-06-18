const state = {
  initialized: false,
  unlocked: false,
  profiles: [],
  settings: { openconnect_path: "openconnect" },
  status: null,
  selectedId: "",
  authMode: "unlock",
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
  if (firstRender) {
    await Promise.all([loadProfiles(), loadSettings(), loadLogs()]);
  }
}

function renderAuth(mode) {
  state.authMode = mode;
  appView.hidden = true;
  authView.hidden = false;
  authSubmit.textContent = mode === "setup" ? "Создать" : "Открыть";
  authSubtitle.textContent = mode === "setup" ? "Создай 4-значный PIN" : "Введи 4-значный PIN";
  pinInput.value = "";
  pinInput.focus();
}

function renderAppShell() {
  authView.hidden = true;
  appView.hidden = false;
  $("vaultPath").textContent = state.status?.vault_path || "";
}

function updateVpnStatus(vpn) {
  const chip = $("vpnState");
  const status = vpn?.state || "disconnected";
  chip.textContent = status;
  chip.className = `state-chip ${status}`;
  $("currentProfile").textContent = vpn?.profile_name || "Нет подключения";
  $("currentPid").textContent = vpn?.pid ? String(vpn.pid) : "-";
  $("currentSince").textContent = vpn?.started_at ? formatTime(vpn.started_at) : "-";
  $("disconnectBtn").disabled = status === "disconnected";
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
  $("logsOutput").textContent = lines.length ? lines.join("\n") : "Лог пуст";
}

function renderProfiles() {
  profilesList.replaceChildren();
  if (state.profiles.length === 0) {
    const empty = document.createElement("div");
    empty.className = "empty";
    empty.textContent = "Профилей пока нет";
    profilesList.append(empty);
    return;
  }

  for (const profile of state.profiles) {
    const item = document.createElement("article");
    item.className = `profile-item ${profile.id === state.selectedId ? "active" : ""}`;

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
    connect.textContent = "Connect";
    connect.addEventListener("click", () => connectProfile(profile.id));

    const edit = document.createElement("button");
    edit.type = "button";
    edit.className = "secondary";
    edit.textContent = "Edit";
    edit.addEventListener("click", () => editProfile(profile));

    const del = document.createElement("button");
    del.type = "button";
    del.className = "danger icon-button";
    del.textContent = "×";
    del.title = "Удалить";
    del.addEventListener("click", () => deleteProfile(profile));

    actions.append(connect, edit, del);
    item.append(main, actions);
    profilesList.append(item);
  }
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
  renderProfiles();
}

function clearEditor() {
  state.selectedId = "";
  $("editorTitle").textContent = "Новый профиль";
  profileForm.reset();
  $("profileId").value = "";
  $("profilePassword").placeholder = "";
  renderProfiles();
}

async function saveProfile() {
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
  editProfile(data.profile);
}

async function connectProfile(id) {
  try {
    await api(`/api/connect/${encodeURIComponent(id)}`, { method: "POST", body: "{}" });
    showToast("OpenConnect запущен");
    await refreshStatus();
    await loadLogs();
  } catch (error) {
    showToast(error.message);
    await loadLogs().catch(() => {});
  }
}

async function deleteProfile(profile) {
  const ok = window.confirm(`Удалить профиль "${profile.name}"?`);
  if (!ok) {
    return;
  }
  await api(`/api/profiles/${encodeURIComponent(profile.id)}`, { method: "DELETE" });
  if (state.selectedId === profile.id) {
    clearEditor();
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

authForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  authError.textContent = "";
  const pin = pinInput.value.trim();
  const path = state.authMode === "setup" ? "/api/setup" : "/api/unlock";
  try {
    await api(path, { method: "POST", body: JSON.stringify({ pin }) });
    await refreshStatus();
    showToast(state.authMode === "setup" ? "Vault создан" : "Vault открыт");
  } catch (error) {
    authError.textContent = error.message;
  }
});

profileForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  try {
    await saveProfile();
  } catch (error) {
    showToast(error.message);
  }
});

$("newProfileBtn").addEventListener("click", clearEditor);
$("clearEditorBtn").addEventListener("click", clearEditor);
$("refreshLogsBtn").addEventListener("click", loadLogs);

$("connectEditorBtn").addEventListener("click", async () => {
  const id = $("profileId").value.trim();
  if (!id) {
    await saveProfile();
  }
  const nextId = $("profileId").value.trim() || state.selectedId;
  if (nextId) {
    await connectProfile(nextId);
  }
});

$("disconnectBtn").addEventListener("click", async () => {
  await api("/api/disconnect", { method: "POST", body: "{}" });
  showToast("OpenConnect остановлен");
  await refreshStatus();
  await loadLogs();
});

$("lockBtn").addEventListener("click", async () => {
  await api("/api/lock", { method: "POST", body: "{}" });
  await refreshStatus();
});

$("saveSettingsBtn").addEventListener("click", async () => {
  const payload = { openconnect_path: $("openconnectPath").value.trim() || "openconnect" };
  state.settings = await api("/api/settings", {
    method: "POST",
    body: JSON.stringify(payload),
  });
  showToast("Настройки сохранены");
  await refreshStatus();
});

window.setInterval(async () => {
  if (!state.unlocked) {
    return;
  }
  try {
    await refreshStatus();
  } catch {
    state.unlocked = false;
  }
}, 2500);

window.setInterval(async () => {
  if (state.unlocked) {
    await loadLogs().catch(() => {});
  }
}, 3500);

refreshStatus().catch((error) => {
  authSubtitle.textContent = "Ошибка запуска";
  authError.textContent = error.message;
});
