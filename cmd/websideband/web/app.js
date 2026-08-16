"use strict";

const STORAGE_KEY = "websideband.prototype.v1";
const navItems = [
  { id: "conversations", label: "Conversations", eyebrow: "Messages", icon: '<path d="M21 15a4 4 0 0 1-4 4H8l-5 3V7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4z"/>' },
  { id: "contacts", label: "Contacts", eyebrow: "Address book", icon: '<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM22 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75"/>' },
  { id: "network", label: "Network", eyebrow: "Reticulum", icon: '<circle cx="12" cy="12" r="2"/><path d="M16.24 7.76a6 6 0 0 1 0 8.48M7.76 16.24a6 6 0 0 1 0-8.48M19.07 4.93a10 10 0 0 1 0 14.14M4.93 19.07a10 10 0 0 1 0-14.14"/>' },
  { id: "settings", label: "Settings", eyebrow: "Preferences", icon: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06-2.83 2.83-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21h-4v-.09a1.65 1.65 0 0 0-1-1.51 1.65 1.65 0 0 0-1.82.33l-.06.06-2.83-2.83.06-.06A1.65 1.65 0 0 0 4.6 15a1.65 1.65 0 0 0-1.51-1H3v-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06 2.83-2.83.06.06A1.65 1.65 0 0 0 8.92 4a1.65 1.65 0 0 0 1-1.51V2h4v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06 2.83 2.83-.06.06A1.65 1.65 0 0 0 19.4 9c.12.6.64 1 1.25 1H21v4h-.09c-.61 0-1.13.4-1.51 1z"/>' }
];

const defaultDeployment = { bridgeHost: "127.0.0.1", bridgeListenAddress: "0.0.0.0", bridgePort: 8081, webListenAddress: "127.0.0.1", webPort: 8080, sharedPort: 37428, controlPort: 37429 };
const defaultNotifications = { privacy: "name", sound: true, vibration: true };
const defaultState = { contacts: [], messages: [], announces: [], unread: {}, notifications: defaultNotifications, deliveryMethod: "direct", activePeer: null, deployment: defaultDeployment };
let state = loadState();
let status = null;
let eventSource = null;
let toastTimer = null;
let eventReconnectTimer = null;
let voiceRecorder = null;
let voiceStream = null;
let voiceChunks = [];
let voiceTimeout = null;
let scannerStream = null;
let scannerTimer = null;
let scannerBusy = false;
let appStarted = false;
let authenticationConfigured = false;
let authenticationState = null;
let deferredInstallPrompt = null;
let alertAudioContext = null;
const pendingStatuses = new Map();

const elements = {
  authScreen: document.querySelector("[data-auth-screen]"),
  authForm: document.querySelector("[data-auth-form]"),
  authEyebrow: document.querySelector("[data-auth-eyebrow]"),
  authTitle: document.querySelector("[data-auth-title]"),
  authIntro: document.querySelector("[data-auth-intro]"),
  authSubmit: document.querySelector("[data-auth-submit]"),
  authError: document.querySelector("[data-auth-error]"),
  views: [...document.querySelectorAll("[data-view]")],
  title: document.querySelector("[data-view-title]"),
  eyebrow: document.querySelector("[data-view-eyebrow]"),
  conversations: document.querySelector("[data-conversation-list]"),
  contacts: document.querySelector("[data-contact-list]"),
  contactCount: document.querySelector("[data-contact-count]"),
  contactForm: document.querySelector("[data-contact-form]"),
  contactNameInput: document.querySelector("#contact-name"),
  contactAddressInput: document.querySelector("#contact-address"),
  contactSearch: document.querySelector("[data-contact-search]"),
  chatPanel: document.querySelector("[data-chat-panel]"),
  chatEmpty: document.querySelector("[data-chat-empty]"),
  chatContent: document.querySelector("[data-chat-content]"),
  chatName: document.querySelector("[data-chat-name]"),
  chatAddress: document.querySelector("[data-chat-address]"),
  chatAvatar: document.querySelector("[data-chat-avatar]"),
  chatSaveContact: document.querySelector("[data-save-chat-contact]"),
  messageList: document.querySelector("[data-message-list]"),
  composer: document.querySelector("[data-composer]"),
  messageInput: document.querySelector("[data-message-input]"),
  voiceButton: document.querySelector("[data-record-voice]"),
  imageButton: document.querySelector("[data-choose-image]"),
  imageInput: document.querySelector("[data-image-input]"),
  ownAddresses: [...document.querySelectorAll("[data-own-address], [data-setting-address]")],
  ownQR: document.querySelector("[data-own-qr]"),
  ownQRFrame: document.querySelector("[data-own-qr-frame]"),
  settingName: document.querySelector("[data-setting-name]"),
  identityNameForm: document.querySelector("[data-identity-name-form]"),
  authUser: document.querySelector("[data-auth-user]"),
  transportDetail: document.querySelector("[data-transport-detail]"),
  transportState: document.querySelector("[data-transport-state]"),
  notificationDetail: document.querySelector("[data-notification-detail]"),
  notificationButton: document.querySelector("[data-enable-notifications]"),
  notificationPrivacy: document.querySelector("[data-notification-privacy]"),
  notificationSound: document.querySelector("[data-notification-sound]"),
  notificationVibration: document.querySelector("[data-notification-vibration]"),
  installDetail: document.querySelector("[data-install-detail]"),
  installButton: document.querySelector("[data-install-app]"),
  offlineBanner: document.querySelector("[data-offline-banner]"),
  sharedInstance: document.querySelector("[data-shared-instance]"),
  networkState: document.querySelector("[data-network-state]"),
  networkSummary: document.querySelector("[data-network-summary]"),
  networkPill: document.querySelector("[data-network-pill]"),
  statusHero: document.querySelector(".status-hero"),
  connectionDot: document.querySelector("[data-connection-dot]"),
  connectionLabel: document.querySelector("[data-connection-label]"),
  connectionDetail: document.querySelector("[data-connection-detail]"),
  eventsState: document.querySelector("[data-events-state]"),
  announceList: document.querySelector("[data-announce-list]"),
  announceCount: document.querySelector("[data-announce-count]"),
  deliveryMethod: document.querySelector("[data-delivery-method]"),
  configurationForm: document.querySelector("[data-configuration-form]"),
  configurationOutput: document.querySelector("[data-configuration-output]"),
  scannerDialog: document.querySelector("[data-scanner-dialog]"),
  scannerVideo: document.querySelector("[data-scanner-video]"),
  scannerCanvas: document.querySelector("[data-scanner-canvas]"),
  scannerPlaceholder: document.querySelector("[data-scanner-placeholder]"),
  scannerStatus: document.querySelector("[data-scanner-status]"),
  qrFileInput: document.querySelector("[data-qr-file]"),
  toast: document.querySelector("[data-toast]")
};

function loadState() {
  try {
    const stored = JSON.parse(localStorage.getItem(STORAGE_KEY) || "{}");
    const deployment = { ...defaultDeployment, ...(stored.deployment || {}) };
    if (stored.deployment?.reticulumHost && !stored.deployment?.bridgeHost) deployment.bridgeHost = stored.deployment.reticulumHost;
    delete deployment.reticulumHost;
    return { ...defaultState, ...stored, deployment, unread: stored.unread || {}, notifications: { ...defaultNotifications, ...(stored.notifications || {}) } };
  } catch (_) {
    return { ...defaultState };
  }
}

function saveState() {
  state.messages = state.messages.slice(-500);
  state.announces = state.announces.slice(0, 100);
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
  } catch (_) {
    const lightweight = { ...state, messages: state.messages.map(({ audioURL, imageURL, ...message }) => message) };
    localStorage.setItem(STORAGE_KEY, JSON.stringify(lightweight));
  }
}

async function loadPersistentData() {
  try {
    await Promise.all(state.contacts.map(contact => fetch("/api/v1/contacts", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(contact)
    }).catch(() => null)));
    const [contactsResponse, messagesResponse] = await Promise.all([
      fetch("/api/v1/contacts", { headers: { Accept: "application/json" } }),
      fetch("/api/v1/messages", { headers: { Accept: "application/json" } })
    ]);
    if (contactsResponse.ok) {
      const payload = await contactsResponse.json();
      state.contacts = payload.contacts || [];
    }
    if (messagesResponse.ok) {
      const payload = await messagesResponse.json();
      (payload.messages || []).forEach(stored => {
        const candidate = state.messages.find(message => message.id === stored.lxmf_id || message.id === stored.id || (stored.request_id && message.requestId === stored.request_id));
        const durable = {
          id: stored.lxmf_id || stored.id,
          requestId: stored.request_id || null,
          peer: stored.peer,
          direction: stored.direction,
          content: stored.content,
          timestamp: stored.timestamp,
          state: stored.state,
          imageURL: stored.image_url || null,
          audioURL: stored.audio_url || null
        };
        if (candidate) Object.assign(candidate, durable);
        else state.messages.push(durable);
      });
      state.unread = Object.fromEntries((payload.conversations || []).map(conversation => [conversation.peer, conversation.unread || 0]));
    }
    saveState();
    renderAll();
    openRequestedConversation();
  } catch (_) {
    showToast("Persistent history is temporarily unavailable");
  }
}

async function persistContact(contact) {
  const response = await fetch("/api/v1/contacts", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(contact)
  });
  if (!response.ok) throw new Error("Contact could not be saved");
}

function makeNav() {
  document.querySelectorAll(".nav-list").forEach(nav => {
    navItems.forEach(item => {
      const button = document.createElement("button");
      button.type = "button";
      button.dataset.navigate = item.id;
      button.innerHTML = `<svg viewBox="0 0 24 24" aria-hidden="true">${item.icon}</svg><span class="nav-label">${item.label}</span>${item.id === "conversations" ? '<span class="unread-badge nav-unread hidden" data-nav-unread></span>' : ""}`;
      nav.append(button);
    });
  });
}

function navigate(viewID) {
  const item = navItems.find(candidate => candidate.id === viewID) || navItems[0];
  elements.views.forEach(view => view.classList.toggle("active", view.dataset.view === item.id));
  document.querySelectorAll("[data-navigate]").forEach(button => button.classList.toggle("active", button.dataset.navigate === item.id));
  elements.title.textContent = item.label;
  elements.eyebrow.textContent = item.eyebrow;
  if (item.id !== "conversations") elements.chatPanel.classList.remove("mobile-open");
}

function contactFor(address) {
  return state.contacts.find(contact => contact.address === address);
}

function contactName(address) {
  return contactFor(address)?.name || `Unknown ${address.slice(0, 6)}`;
}

function initials(name) {
  return name.split(/\s+/).filter(Boolean).slice(0, 2).map(part => part[0]).join("").toUpperCase() || "?";
}

function messagesFor(address) {
  return state.messages.filter(message => message.peer === address).sort((a, b) => a.timestamp - b.timestamp);
}

function renderConversations() {
  elements.conversations.replaceChildren();
  const addresses = new Set(state.messages.map(message => message.peer));
  if (state.activePeer) addresses.add(state.activePeer);
  const rows = [...addresses].map(address => {
    const messages = messagesFor(address);
    return { address, last: messages[messages.length - 1] };
  }).sort((a, b) => (b.last?.timestamp || 0) - (a.last?.timestamp || 0));
  if (!rows.length) {
    elements.conversations.append(emptyList("No conversations yet", "Add a contact to send your first LXMF message."));
    renderUnreadBadges();
    return;
  }
  rows.forEach(({ address, last }) => {
    const name = contactName(address);
    const button = document.createElement("button");
    button.type = "button";
    const unread = Number(state.unread[address] || 0);
    button.className = "conversation-row" + (state.activePeer === address ? " active" : "") + (unread ? " unread" : "");
    button.dataset.peer = address;
    button.append(avatar(initials(name)), rowCopy(name, last?.content || address));
    if (unread) button.append(unreadBadge(unread));
    button.append(meta(last ? relativeTime(last.timestamp) : ""));
    elements.conversations.append(button);
  });
  renderUnreadBadges();
}

function renderContacts() {
  elements.contacts.replaceChildren();
  elements.contactCount.textContent = String(state.contacts.length);
  const query = elements.contactSearch.value.trim().toLowerCase();
  const contacts = state.contacts.filter(contact => !query || contact.name.toLowerCase().includes(query) || contact.address.includes(query));
  if (!state.contacts.length) {
    elements.contacts.append(emptyList("No saved contacts", "Add an LXMF address using the form."));
    return;
  }
  if (!contacts.length) {
    elements.contacts.append(emptyList("No matching contacts", "Try a different name or address."));
    return;
  }
  contacts.slice().sort((a, b) => a.name.localeCompare(b.name)).forEach(contact => {
    const row = document.createElement("div");
    row.className = "contact-row";
    row.append(avatar(initials(contact.name)), rowCopy(contact.name, contact.address));
    const actions = document.createElement("div");
    actions.className = "contact-row-actions";
    actions.append(
      contactAction("message", `Message ${contact.name}`, '<path d="M21 15a4 4 0 0 1-4 4H8l-5 3V7a4 4 0 0 1 4-4h10a4 4 0 0 1 4 4z"/>', contact.address),
      contactAction("edit", `Edit ${contact.name}`, '<path d="M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L8 18l-4 1 1-4z"/>', contact.address),
      contactAction("delete", `Delete ${contact.name}`, '<path d="M3 6h18M8 6V4h8v2M19 6l-1 15H6L5 6M10 11v6M14 11v6"/>', contact.address)
    );
    row.append(actions);
    elements.contacts.append(row);
  });
}

function contactAction(action, label, icon, address) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = `contact-action ${action === "delete" ? "delete-contact" : ""}`;
  button.dataset.contactAction = action;
  button.dataset.address = address;
  button.setAttribute("aria-label", label);
  button.title = label;
  button.innerHTML = `<svg viewBox="0 0 24 24" aria-hidden="true">${icon}</svg>`;
  return button;
}

function openConversation(address) {
  state.activePeer = address;
  saveState();
  navigate("conversations");
  elements.chatPanel.classList.add("mobile-open");
  elements.chatEmpty.classList.add("hidden");
  elements.chatContent.classList.remove("hidden");
  const name = contactName(address);
  elements.chatName.textContent = name;
  elements.chatAddress.textContent = address;
  elements.chatAvatar.textContent = initials(name);
  elements.chatSaveContact.classList.toggle("hidden", Boolean(contactFor(address)));
  renderConversations();
  renderMessages();
  markConversationRead(address);
  requestAnimationFrame(() => elements.messageInput.focus());
}

function renderMessages() {
  elements.messageList.replaceChildren();
  if (!state.activePeer) return;
  const messages = messagesFor(state.activePeer);
  if (!messages.length) {
    elements.messageList.append(emptyList("Start the conversation", "Messages use LXMF encryption and signing."));
    return;
  }
  let renderedDay = "";
  messages.forEach(message => {
    const day = new Date(message.timestamp).toLocaleDateString([], { weekday: "short", month: "short", day: "numeric" });
    if (day !== renderedDay) {
      const separator = document.createElement("div");
      separator.className = "message-day";
      separator.textContent = day;
      elements.messageList.append(separator);
      renderedDay = day;
    }
    const wrapper = document.createElement("article");
    wrapper.className = `message ${message.direction}`;
    const bubble = document.createElement("div");
    bubble.className = "message-bubble";
    bubble.textContent = message.content;
    if (message.imageURL) {
      const image = document.createElement("img");
      image.src = message.imageURL;
      image.alt = message.content === "Image" ? "Shared image" : message.content;
      image.loading = "lazy";
      image.addEventListener("error", () => {
        const warning = document.createElement("span");
        warning.className = "attachment-error";
        warning.textContent = "Image could not be displayed";
        image.replaceWith(warning);
      }, { once: true });
      bubble.append(image);
    }
    if (message.audioURL) {
      const player = document.createElement("audio");
      player.controls = true;
      player.preload = "metadata";
      player.src = message.audioURL;
      player.setAttribute("aria-label", "Voice note");
      bubble.append(player);
    }
    const details = document.createElement("div");
    details.className = "message-meta";
    const timestamp = document.createElement("time");
    timestamp.dateTime = new Date(message.timestamp).toISOString();
    timestamp.textContent = new Date(message.timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    details.append(timestamp);
    if (message.direction === "outgoing") {
      const messageState = document.createElement("span");
      messageState.className = `message-state ${message.state || "queued"}`;
      messageState.textContent = message.state || "queued";
      details.append(messageState);
    }
    wrapper.append(bubble, details);
    elements.messageList.append(wrapper);
  });
  requestAnimationFrame(() => { elements.messageList.scrollTop = elements.messageList.scrollHeight; });
}

function renderAnnounces() {
  elements.announceList.replaceChildren();
  elements.announceCount.textContent = String(state.announces.length);
  if (!state.announces.length) {
    elements.announceList.append(emptyList("No announces observed", "Known LXMF destinations will appear here live."));
    return;
  }
  state.announces.forEach(announce => {
    const row = document.createElement("div");
    row.className = "announce-row";
    const name = announce.display_name || "Unnamed destination";
    row.append(avatar(initials(name)), rowCopy(name, announce.destination));
    const detail = document.createElement("div");
    detail.className = "row-meta";
    detail.textContent = Number.isFinite(announce.hops) ? `${announce.hops} hop${announce.hops === 1 ? "" : "s"}` : "Path seen";
    row.append(detail);
    if (!contactFor(announce.destination)) {
      const add = document.createElement("button");
      add.type = "button";
      add.className = "small-button";
      add.dataset.addAnnounce = announce.destination;
      add.dataset.announceName = name;
      add.textContent = "Add";
      row.append(add);
    }
    elements.announceList.append(row);
  });
}

function renderSettings() {
  elements.deliveryMethod.value = state.deliveryMethod;
  elements.notificationPrivacy.value = state.notifications.privacy;
  elements.notificationSound.checked = state.notifications.sound;
  elements.notificationVibration.checked = state.notifications.vibration;
  const notificationSupported = "Notification" in window && "serviceWorker" in navigator;
  const permission = notificationSupported ? Notification.permission : "unsupported";
  elements.notificationDetail.textContent = permission === "granted" ? "Enabled for new messages while PEREVIA is running." : permission === "denied" ? "Blocked in browser settings." : permission === "default" ? "Permission has not been requested." : "Not supported by this browser.";
  elements.notificationButton.textContent = permission === "granted" ? "Enabled" : permission === "denied" ? "Blocked" : "Enable";
  elements.notificationButton.disabled = permission === "granted" || permission === "denied" || !notificationSupported;
  renderInstallState();
  Object.entries(state.deployment).forEach(([name, value]) => {
    const input = elements.configurationForm.elements.namedItem(name);
    if (input) input.value = value;
  });
  updateConfigurationOutput();
  if (authenticationState) {
    elements.authUser.textContent = authenticationState.disabled ? "Handled by an external trusted layer" : `Signed in as ${authenticationState.username || "administrator"}`;
    const secure = window.isSecureContext;
    elements.transportState.textContent = secure ? "Secure" : "HTTP";
    elements.transportState.classList.toggle("online", secure);
    elements.transportDetail.textContent = secure ? "Browser camera, microphone, and PWA features are available." : "Use trusted HTTPS before exposing this service to other devices.";
  }
  if (status) {
    elements.settingName.textContent = status.display_name || "PEREVIA";
    elements.identityNameForm.elements.namedItem("displayName").value = status.display_name || "PEREVIA";
    elements.ownAddresses.forEach(element => { element.textContent = status.address; element.dataset.copy = status.address; });
    elements.ownQR.src = `/api/v1/qr?address=${encodeURIComponent(status.address)}`;
    elements.ownQR.addEventListener("error", () => elements.ownQRFrame.classList.remove("ready"), { once: true });
    elements.ownQRFrame.classList.add("ready");
    elements.sharedInstance.textContent = `${(status.shared_instance?.type || "tcp").toUpperCase()} · ${status.shared_instance?.port || 37428}`;
  } else {
    elements.ownAddresses.forEach(element => { element.textContent = "Unavailable"; delete element.dataset.copy; });
    elements.ownQR.removeAttribute("src");
    elements.ownQRFrame.classList.remove("ready");
  }
}

function renderConnection(connected, detail) {
  const label = connected ? "Connected" : "Disconnected";
  elements.connectionDot.classList.toggle("online", connected);
  elements.connectionLabel.textContent = label;
  elements.connectionDetail.textContent = detail;
  elements.networkState.textContent = connected ? "Reticulum connected" : "Disconnected";
  elements.networkSummary.textContent = connected ? "Shared RNS instance and LXMF router are available." : "The interface is ready; the LXMF bridge is unavailable.";
  elements.networkPill.textContent = connected ? "Online" : "Offline";
  elements.networkPill.classList.toggle("online", connected);
  elements.statusHero.classList.toggle("connected", connected);
}

function updateOnlineState() {
  const online = navigator.onLine;
  elements.offlineBanner.classList.toggle("hidden", online);
  if (!online) renderConnection(false, "Browser offline");
  else if (appStarted) { refreshStatus(); if (state.activePeer) markConversationRead(state.activePeer); }
}

async function refreshStatus(showFeedback = false) {
  try {
    const response = await fetch("/api/v1/status", { headers: { Accept: "application/json" } });
    if (!response.ok) throw new Error("Bridge unavailable");
    status = await response.json();
    renderConnection(Boolean(status.connected), status.display_name || "LXMF bridge ready");
    renderSettings();
    if (showFeedback) showToast("Network status refreshed");
  } catch (_) {
    status = null;
    renderConnection(false, "Bridge unavailable");
    renderSettings();
    if (showFeedback) showToast("Bridge is not connected yet");
  }
}

function connectEvents() {
  clearTimeout(eventReconnectTimer);
  eventSource?.close();
  eventSource = new EventSource("/api/v1/events");
  eventSource.addEventListener("open", () => {
    elements.eventsState.textContent = "Live";
    elements.eventsState.classList.add("online");
  });
  eventSource.addEventListener("error", () => {
    elements.eventsState.textContent = "Offline";
    elements.eventsState.classList.remove("online");
    eventSource.close();
    eventReconnectTimer = setTimeout(connectEvents, 10000);
  });
  eventSource.addEventListener("ready", event => {
    const payload = parseEvent(event);
    if (payload?.data) { status = payload.data; renderConnection(Boolean(status.connected), status.display_name || "LXMF bridge ready"); renderSettings(); }
  });
  eventSource.addEventListener("identity_updated", event => {
    const payload = parseEvent(event);
    if (payload?.data) { status = payload.data; renderConnection(Boolean(status.connected), status.display_name || "LXMF bridge ready"); renderSettings(); }
  });
  eventSource.addEventListener("message_received", event => {
    const payload = parseEvent(event)?.data;
    if (!payload) return;
    const audioURL = payload.audio?.mode === "opus_ogg" && payload.audio?.data ? `data:audio/ogg;base64,${payload.audio.data}` : null;
    const imageFormats = { webp: "webp", png: "png", jpg: "jpeg", jpeg: "jpeg" };
    const imageFormat = String(payload.image?.format || "").toLowerCase().replace(/^\./, "");
    const imageType = imageFormats[imageFormat];
    const imageURL = imageType && payload.image?.data ? `data:image/${imageType};base64,${payload.image.data}` : null;
    const message = { id: payload.message_id, peer: payload.source, direction: "incoming", content: payload.content, audioURL, imageURL, timestamp: (payload.timestamp || Date.now() / 1000) * 1000, state: "delivered" };
    if (!addMessage(message)) return;
    const isReading = state.activePeer === payload.source && document.visibilityState === "visible" && document.hasFocus();
    if (isReading) markConversationRead(payload.source);
    else {
      state.unread[payload.source] = Number(state.unread[payload.source] || 0) + 1;
      saveState(); renderConversations(); renderUnreadBadges();
      alertIncomingMessage(message);
    }
    showToast(`Message from ${contactName(payload.source)}`);
  });
  eventSource.addEventListener("message_status", event => {
    const payload = parseEvent(event)?.data;
    if (!payload) return;
    const message = state.messages.find(candidate => (payload.request_id && candidate.requestId === payload.request_id) || (payload.message_id && candidate.id === payload.message_id));
    if (message) {
      message.state = payload.state;
      if (payload.message_id) message.id = payload.message_id;
      saveState(); renderConversations(); renderMessages();
    } else if (payload.request_id) pendingStatuses.set(payload.request_id, payload);
  });
  eventSource.addEventListener("announce", event => {
    const payload = parseEvent(event)?.data;
    if (!payload) return;
    state.announces = [payload, ...state.announces.filter(item => item.destination !== payload.destination)].slice(0, 100);
    saveState(); renderAnnounces();
  });
}

function parseEvent(event) {
  try { return JSON.parse(event.data); } catch (_) { return null; }
}

function addMessage(message) {
  if (message.id && state.messages.some(existing => existing.id === message.id)) return false;
  state.messages.push(message);
  saveState();
  renderConversations();
  if (state.activePeer === message.peer) renderMessages();
  return true;
}

async function sendMessage(content, options = {}) {
  const peer = state.activePeer;
  if (!peer) return;
  const localID = createLocalMessageID();
  const message = { id: localID, requestId: null, peer, direction: "outgoing", content, audioURL: options.audioURL || null, imageURL: options.imageURL || null, timestamp: Date.now(), state: "queued" };
  addMessage(message);
  try {
    const response = await fetch("/api/v1/messages", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify({ destination: peer, content, method: state.deliveryMethod, ...(options.audio ? { audio: options.audio } : {}), ...(options.image ? { image: options.image } : {}) })
    });
    const result = await response.json();
    if (!response.ok) throw new Error(result.error || "Message could not be queued");
    message.requestId = result.request_id;
    if (result.message_id) message.id = result.message_id;
    message.state = result.state || "queued";
    const pending = pendingStatuses.get(message.requestId);
    if (pending) {
      message.state = pending.state;
      if (pending.message_id) message.id = pending.message_id;
      pendingStatuses.delete(message.requestId);
    }
  } catch (error) {
    message.state = "failed";
    showToast(error.message || "Bridge unavailable");
  }
  saveState(); renderConversations(); renderMessages();
}

function createLocalMessageID() {
  if (typeof globalThis.crypto?.randomUUID === "function") return globalThis.crypto.randomUUID();
  if (typeof globalThis.crypto?.getRandomValues === "function") {
    const bytes = new Uint8Array(16);
    globalThis.crypto.getRandomValues(bytes);
    return `local-${Array.from(bytes, byte => byte.toString(16).padStart(2, "0")).join("")}`;
  }
  return `local-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

async function toggleVoiceRecording() {
  if (voiceRecorder && voiceRecorder.state !== "inactive") {
    voiceRecorder.stop();
    return;
  }
  if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === "undefined") {
    showToast("Voice recording is not supported by this browser");
    return;
  }
  if (!state.activePeer) return;
  try {
    voiceStream = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true } });
    const candidates = ["audio/webm;codecs=opus", "audio/mp4", "audio/ogg;codecs=opus"];
    const mimeType = candidates.find(candidate => MediaRecorder.isTypeSupported(candidate));
    voiceRecorder = mimeType ? new MediaRecorder(voiceStream, { mimeType }) : new MediaRecorder(voiceStream);
    voiceChunks = [];
    voiceRecorder.addEventListener("dataavailable", event => { if (event.data.size) voiceChunks.push(event.data); });
    voiceRecorder.addEventListener("stop", finishVoiceRecording, { once: true });
    voiceRecorder.start(1000);
    elements.voiceButton.classList.add("recording");
    elements.voiceButton.setAttribute("aria-pressed", "true");
    elements.voiceButton.setAttribute("aria-label", "Stop voice recording");
    elements.messageInput.placeholder = "Recording voice note… tap microphone to stop";
    voiceTimeout = setTimeout(() => { if (voiceRecorder?.state === "recording") voiceRecorder.stop(); }, 60000);
  } catch (_) {
    showToast(window.isSecureContext ? "Microphone permission was not granted" : "Voice recording requires HTTPS or localhost");
    resetVoiceRecorder();
  }
}

async function finishVoiceRecording() {
  clearTimeout(voiceTimeout);
  const recording = new Blob(voiceChunks, { type: voiceRecorder?.mimeType || voiceChunks[0]?.type || "audio/webm" });
  resetVoiceRecorder();
  if (!recording.size) { showToast("No audio was recorded"); return; }
  elements.voiceButton.disabled = true;
  elements.voiceButton.classList.add("processing");
  elements.messageInput.placeholder = "Preparing voice note…";
  try {
    const response = await fetch("/api/v1/audio/transcode", { method: "POST", headers: { "Content-Type": recording.type || "audio/webm" }, body: recording });
    if (!response.ok) {
      const error = await response.json().catch(() => ({}));
      throw new Error(error.error || "Voice note could not be converted");
    }
    const bytes = new Uint8Array(await response.arrayBuffer());
    const encoded = bytesToBase64(bytes);
    await sendMessage("Voice message", { audio: { mode: "opus_ogg", data: encoded }, audioURL: `data:audio/ogg;base64,${encoded}` });
  } catch (error) {
    showToast(error.message || "Voice note could not be sent");
  } finally {
    elements.voiceButton.disabled = false;
    elements.voiceButton.classList.remove("processing");
    elements.messageInput.placeholder = "Message…";
  }
}

function resetVoiceRecorder() {
  clearTimeout(voiceTimeout);
  voiceStream?.getTracks().forEach(track => track.stop());
  voiceStream = null;
  voiceRecorder = null;
  voiceChunks = [];
  elements.voiceButton.classList.remove("recording");
  elements.voiceButton.setAttribute("aria-pressed", "false");
  elements.voiceButton.setAttribute("aria-label", "Record voice note");
  elements.messageInput.placeholder = "Message…";
}

function bytesToBase64(bytes) {
  let encoded = "";
  const chunkSize = 24576;
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    encoded += btoa(String.fromCharCode(...bytes.subarray(offset, offset + chunkSize)));
  }
  return encoded;
}

async function prepareAndSendImage(file) {
  if (!file || !file.type.startsWith("image/") || !state.activePeer) return;
  elements.imageButton.disabled = true;
  elements.imageButton.classList.add("processing");
  showToast("Preparing image…");
  try {
    const response = await fetch("/api/v1/images/prepare", { method: "POST", headers: { "Content-Type": file.type }, body: file });
    if (!response.ok) {
      const error = await response.json().catch(() => ({}));
      throw new Error(error.error || "Image could not be prepared");
    }
    const bytes = new Uint8Array(await response.arrayBuffer());
    const encoded = bytesToBase64(bytes);
    const caption = elements.messageInput.value.trim() || "Image";
    elements.messageInput.value = "";
    elements.messageInput.style.height = "auto";
    await sendMessage(caption, { image: { format: "webp", data: encoded }, imageURL: `data:image/webp;base64,${encoded}` });
  } catch (error) {
    showToast(error.message || "Image could not be sent");
  } finally {
    elements.imageInput.value = "";
    elements.imageButton.disabled = false;
    elements.imageButton.classList.remove("processing");
  }
}

async function sendAnnounce() {
  try {
    const response = await fetch("/api/v1/announce", { method: "POST" });
    if (!response.ok) throw new Error();
    showToast("LXMF announce sent");
  } catch (_) {
    showToast("Bridge unavailable—announce not sent");
  }
}

async function saveDisplayName() {
  const displayName = elements.identityNameForm.elements.namedItem("displayName").value.trim();
  if (!displayName) return;
  const submit = elements.identityNameForm.querySelector("button[type=submit]");
  submit.disabled = true;
  try {
    const response = await fetch("/api/v1/settings/identity", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ display_name: displayName })
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(payload.error || "Display name could not be saved");
    status = payload;
    renderSettings();
    renderConnection(Boolean(status.connected), status.display_name || "LXMF bridge ready");
    showToast("Display name saved and announced");
  } catch (error) {
    showToast(error.message);
  } finally {
    submit.disabled = false;
  }
}

async function checkAuthentication() {
  try {
    const response = await fetch("/api/v1/auth/status", { cache: "no-store" });
    if (!response.ok) throw new Error();
    const auth = await response.json();
    authenticationState = auth;
    if (auth.authenticated) {
      elements.authScreen.classList.add("hidden");
      startApplication();
      return;
    }
    authenticationConfigured = auth.configured;
    elements.authEyebrow.textContent = auth.configured ? "Welcome back" : "First-time setup";
    elements.authTitle.textContent = auth.configured ? "Sign in" : "Create your login";
    elements.authIntro.textContent = auth.configured ? "Sign in to open your conversations." : "Protect messages and settings with an administrator login. Use at least 12 characters.";
    elements.authSubmit.textContent = auth.configured ? "Sign in" : "Create secure login";
    elements.authForm.elements.namedItem("username").value = auth.username || "admin";
    elements.authForm.elements.namedItem("password").autocomplete = auth.configured ? "current-password" : "new-password";
    elements.authForm.classList.remove("hidden");
    elements.authForm.elements.namedItem(auth.configured ? "password" : "username").focus();
  } catch (_) {
    elements.authTitle.textContent = "PEREVIA unavailable";
    elements.authIntro.textContent = "The local service could not be reached. Try refreshing this page.";
  }
}

async function submitAuthentication() {
  const data = new FormData(elements.authForm);
  const endpoint = authenticationConfigured ? "login" : "setup";
  elements.authSubmit.disabled = true;
  elements.authError.textContent = "";
  try {
    const response = await fetch(`/api/v1/auth/${endpoint}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username: String(data.get("username")).trim(), password: String(data.get("password")) })
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(payload.error || "Sign in failed");
    elements.authForm.reset();
    elements.authScreen.classList.add("hidden");
    startApplication();
  } catch (error) {
    elements.authError.textContent = error.message;
  } finally {
    elements.authSubmit.disabled = false;
  }
}

async function logout() {
  await fetch("/api/v1/auth/logout", { method: "POST" }).catch(() => {});
  eventSource?.close();
  window.location.reload();
}

function startApplication() {
  if (appStarted) return;
  appStarted = true;
  refreshStatus();
  connectEvents();
  loadPersistentData();
}

async function markConversationRead(address) {
  if (!address || !state.unread[address]) return;
  const previousUnread = state.unread[address];
  state.unread[address] = 0;
  saveState();
  renderConversations();
  renderUnreadBadges();
  try {
    const response = await fetch(`/api/v1/conversations/${address}/read`, { method: "PUT" });
    if (!response.ok) throw new Error();
  } catch (_) {
    state.unread[address] = previousUnread;
    saveState(); renderConversations(); renderUnreadBadges();
    showToast("Read state will retry after reconnecting");
  }
}

function totalUnread() {
  return Object.values(state.unread).reduce((total, count) => total + Number(count || 0), 0);
}

function renderUnreadBadges() {
  const total = totalUnread();
  document.querySelectorAll("[data-nav-unread]").forEach(badge => {
    badge.textContent = total > 99 ? "99+" : String(total);
    badge.classList.toggle("hidden", total === 0);
  });
  document.title = total ? `(${total > 99 ? "99+" : total}) PEREVIA` : "PEREVIA";
}

function unreadBadge(count) {
  const badge = document.createElement("span");
  badge.className = "unread-badge";
  badge.textContent = count > 99 ? "99+" : String(count);
  badge.setAttribute("aria-label", `${count} unread message${count === 1 ? "" : "s"}`);
  return badge;
}

async function enableNotifications() {
  if (!("Notification" in window)) {
    showToast("Notifications are not supported by this browser");
    return;
  }
  const permission = await Notification.requestPermission();
  if (permission === "granted") {
    prepareAlertSound();
    showToast("Message notifications enabled");
  } else {
    showToast("Notifications were not enabled");
  }
  renderSettings();
}

async function alertIncomingMessage(message) {
  if (state.notifications.sound) playAlertSound();
  if (state.notifications.vibration && navigator.vibrate) navigator.vibrate([120, 70, 120]);
  if (!("Notification" in window) || Notification.permission !== "granted" || (document.visibilityState === "visible" && document.hasFocus())) return;
  const name = contactName(message.peer);
  const privacy = state.notifications.privacy;
  const title = privacy === "hidden" ? "New PEREVIA message" : name;
  const body = privacy === "full" ? (message.content || (message.imageURL ? "Image" : message.audioURL ? "Voice note" : "New message")) : privacy === "name" ? "New LXMF message" : "Open PEREVIA to view it";
  try {
    const registration = await navigator.serviceWorker.ready;
    await registration.showNotification(title, { body, icon: "/icon.svg", badge: "/icon.svg", tag: `conversation-${message.peer}`, renotify: true, data: { address: message.peer } });
  } catch (_) {}
}

function prepareAlertSound() {
  if (!state.notifications.sound || alertAudioContext) return;
  const AudioContext = window.AudioContext || window.webkitAudioContext;
  if (AudioContext) alertAudioContext = new AudioContext();
}

function playAlertSound() {
  prepareAlertSound();
  if (!alertAudioContext) return;
  alertAudioContext.resume().then(() => {
    const oscillator = alertAudioContext.createOscillator();
    const gain = alertAudioContext.createGain();
    oscillator.frequency.setValueAtTime(620, alertAudioContext.currentTime);
    gain.gain.setValueAtTime(.0001, alertAudioContext.currentTime);
    gain.gain.exponentialRampToValueAtTime(.12, alertAudioContext.currentTime + .02);
    gain.gain.exponentialRampToValueAtTime(.0001, alertAudioContext.currentTime + .18);
    oscillator.connect(gain).connect(alertAudioContext.destination);
    oscillator.start();
    oscillator.stop(alertAudioContext.currentTime + .2);
  }).catch(() => {});
}

function renderInstallState() {
  const installed = window.matchMedia("(display-mode: standalone)").matches || window.navigator.standalone === true;
  const isiOS = /iphone|ipad|ipod/i.test(navigator.userAgent);
  elements.installButton.classList.toggle("hidden", installed || (!deferredInstallPrompt && !isiOS));
  elements.installButton.textContent = deferredInstallPrompt ? "Install app" : "How to install";
  elements.installDetail.textContent = installed ? "PEREVIA is installed on this device." : isiOS && !deferredInstallPrompt ? "In Safari, tap Share, then Add to Home Screen." : deferredInstallPrompt ? "Install PEREVIA for a full-screen app experience and faster access." : "Installation becomes available when this browser supports the PWA install prompt.";
}

async function installApplication() {
  if (!deferredInstallPrompt) {
    showToast("On iPhone, use Safari Share → Add to Home Screen");
    return;
  }
  deferredInstallPrompt.prompt();
  await deferredInstallPrompt.userChoice;
  deferredInstallPrompt = null;
  renderInstallState();
}

function openRequestedConversation() {
  const url = new URL(window.location.href);
  const address = normalizedAddress(url.searchParams.get("conversation"));
  if (!address) return;
  url.searchParams.delete("conversation");
  history.replaceState(null, "", url.pathname + url.search + url.hash);
  openConversation(address);
}

function normalizedAddress(value) {
  const match = String(value || "").trim().match(/(?:lxmf:\/\/|lxmf:|rns:\/\/|rns:)?([0-9a-f]{32})/i);
  return match ? match[1].toLowerCase() : "";
}

async function pasteContactAddress() {
  try {
    const address = normalizedAddress(await navigator.clipboard.readText());
    if (!address) throw new Error("Clipboard does not contain an LXMF address");
    acceptScannedAddress(address);
  } catch (error) {
    showToast(error.message || "Clipboard access was denied");
  }
}

function acceptScannedAddress(address, suggestedName = "") {
  const normalized = normalizedAddress(address);
  if (!normalized) {
    showToast("No valid LXMF address found");
    return;
  }
  stopScanner();
  if (elements.scannerDialog.open) elements.scannerDialog.close();
  navigate("contacts");
  elements.contactAddressInput.value = normalized;
  const existing = contactFor(normalized);
  elements.contactNameInput.value = existing?.name || suggestedName || "";
  elements.contactNameInput.focus();
  showToast(existing ? "Contact loaded for editing" : "Address scanned—add a name");
}

async function shareIdentity() {
  if (!status?.address) {
    showToast("Connect the bridge to load your address");
    return;
  }
  const text = `Add me on LXMF: ${status.address}`;
  if (navigator.share) {
    try {
      await navigator.share({ title: "My LXMF address", text });
      return;
    } catch (error) {
      if (error.name === "AbortError") return;
    }
  }
  copyText(status.address, "LXMF address copied");
}

async function openScanner() {
  elements.scannerDialog.showModal();
  elements.scannerStatus.textContent = "Starting camera…";
  elements.scannerPlaceholder.classList.remove("camera-ready");
  if (!navigator.mediaDevices?.getUserMedia) {
    elements.scannerStatus.textContent = "Camera scanning is unavailable here. Choose a QR image instead.";
    return;
  }
  try {
    scannerStream = await navigator.mediaDevices.getUserMedia({ video: { facingMode: { ideal: "environment" } }, audio: false });
    elements.scannerVideo.srcObject = scannerStream;
    await elements.scannerVideo.play();
    elements.scannerPlaceholder.classList.add("camera-ready");
    elements.scannerStatus.textContent = "Looking for an LXMF address…";
    scheduleQRScan(200);
  } catch (_) {
    elements.scannerStatus.textContent = "Camera access failed. Choose a QR image or paste the address.";
  }
}

function stopScanner() {
  clearTimeout(scannerTimer);
  scannerTimer = null;
  scannerBusy = false;
  scannerStream?.getTracks().forEach(track => track.stop());
  scannerStream = null;
  elements.scannerVideo.srcObject = null;
}

function scheduleQRScan(delay = 650) {
  clearTimeout(scannerTimer);
  scannerTimer = setTimeout(scanCameraFrame, delay);
}

async function scanCameraFrame() {
  if (!scannerStream || scannerBusy || !elements.scannerDialog.open) return;
  const video = elements.scannerVideo;
  if (!video.videoWidth || !video.videoHeight) {
    scheduleQRScan();
    return;
  }
  scannerBusy = true;
  const canvas = elements.scannerCanvas;
  const scale = Math.min(1, 960 / video.videoWidth);
  canvas.width = Math.round(video.videoWidth * scale);
  canvas.height = Math.round(video.videoHeight * scale);
  canvas.getContext("2d", { alpha: false }).drawImage(video, 0, 0, canvas.width, canvas.height);
  const blob = await new Promise(resolve => canvas.toBlob(resolve, "image/jpeg", .78));
  if (blob) {
    const address = await decodeQRImage(blob, false);
    if (address) {
      acceptScannedAddress(address);
      scannerBusy = false;
      return;
    }
  }
  scannerBusy = false;
  scheduleQRScan();
}

async function decodeQRImage(file, showErrors = true) {
  try {
    const response = await fetch("/api/v1/qr/decode", { method: "POST", headers: { "Content-Type": file.type || "image/jpeg" }, body: file });
    if (!response.ok) {
      const payload = await response.json().catch(() => ({}));
      throw new Error(payload.error || "QR code could not be read");
    }
    return (await response.json()).address;
  } catch (error) {
    if (showErrors) showToast(error.message || "QR code could not be read");
    return "";
  }
}

async function chooseQRImage(file) {
  if (!file) return;
  elements.scannerStatus.textContent = "Reading QR image…";
  const address = await decodeQRImage(file);
  elements.qrFileInput.value = "";
  if (address) acceptScannedAddress(address);
  else elements.scannerStatus.textContent = "No LXMF address found. Try a clearer image.";
}

async function deleteConversation() {
  const address = state.activePeer;
  if (!address) return;
  const name = contactName(address);
  if (!window.confirm(`Delete the conversation with ${name}? Messages and stored media will be permanently removed. The contact will be kept.`)) return;
  const button = document.querySelector("[data-delete-conversation]");
  button.disabled = true;
  try {
    const response = await fetch(`/api/v1/conversations/${address}`, { method: "DELETE" });
    if (!response.ok) throw new Error("Conversation could not be deleted");
    state.messages = state.messages.filter(message => message.peer !== address);
    state.activePeer = null;
    saveState();
    elements.chatContent.classList.add("hidden");
    elements.chatEmpty.classList.remove("hidden");
    elements.chatPanel.classList.remove("mobile-open");
    renderConversations();
    showToast("Conversation deleted");
  } catch (error) {
    showToast(error.message);
  } finally {
    button.disabled = false;
  }
}

function editContact(address) {
  const contact = contactFor(address);
  if (!contact) return;
  navigate("contacts");
  elements.contactNameInput.value = contact.name;
  elements.contactAddressInput.value = contact.address;
  elements.contactNameInput.focus();
}

function stageAnnouncedContact(address, name) {
  navigate("contacts");
  elements.contactAddressInput.value = address;
  elements.contactNameInput.value = name === "Unnamed destination" ? "" : name;
  (elements.contactNameInput.value ? elements.contactForm.querySelector("button[type=submit]") : elements.contactNameInput).focus();
  showToast("Destination ready to save");
}

function bindEvents() {
  document.addEventListener("click", event => {
    if (state.notifications.sound) prepareAlertSound();
    const nav = event.target.closest("[data-navigate]");
    if (nav) navigate(nav.dataset.navigate);
    const peer = event.target.closest("[data-peer]");
    if (peer) openConversation(peer.dataset.peer);
    const contactActionButton = event.target.closest("[data-contact-action]");
    if (contactActionButton?.dataset.contactAction === "message") openConversation(contactActionButton.dataset.address);
    if (contactActionButton?.dataset.contactAction === "edit") editContact(contactActionButton.dataset.address);
    if (contactActionButton?.dataset.contactAction === "delete") deleteContact(contactActionButton.dataset.address);
    const announced = event.target.closest("[data-add-announce]");
    if (announced) stageAnnouncedContact(announced.dataset.addAnnounce, announced.dataset.announceName);
    if (event.target.closest("[data-open-contacts]")) navigate("contacts");
    if (event.target.closest("[data-chat-back]")) elements.chatPanel.classList.remove("mobile-open");
    if (event.target.closest("[data-refresh]")) refreshStatus(true);
    if (event.target.closest("[data-announce]")) sendAnnounce();
    if (event.target.closest("[data-chat-address]")) copyText(state.activePeer);
    if (event.target.closest("[data-save-chat-contact]")) acceptScannedAddress(state.activePeer);
    if (event.target.closest("[data-delete-conversation]")) deleteConversation();
    const copy = event.target.closest("[data-copy]");
    if (copy) copyText(copy.dataset.copy);
    if (event.target.closest("[data-reset-preferences]")) resetPreferences();
    const generator = event.target.closest("[data-generate-secret]");
    if (generator) generateSecret(generator.dataset.generateSecret);
    if (event.target.closest("[data-copy-configuration]")) copyText(elements.configurationOutput.value, "Configuration copied");
    if (event.target.closest("[data-record-voice]")) toggleVoiceRecording();
    if (event.target.closest("[data-choose-image]")) elements.imageInput.click();
    if (event.target.closest("[data-share-identity]")) shareIdentity();
    if (event.target.closest("[data-copy-own-address]")) copyText(status?.address, "LXMF address copied");
    if (event.target.closest("[data-scan-contact]")) openScanner();
    if (event.target.closest("[data-close-scanner]")) elements.scannerDialog.close();
    if (event.target.closest("[data-pick-qr]")) elements.qrFileInput.click();
    if (event.target.closest("[data-paste-address]")) pasteContactAddress();
    if (event.target.closest("[data-logout]")) logout();
    if (event.target.closest("[data-enable-notifications]")) enableNotifications();
    if (event.target.closest("[data-install-app]")) installApplication();
  });
  elements.authForm.addEventListener("submit", event => { event.preventDefault(); submitAuthentication(); });
  elements.identityNameForm.addEventListener("submit", event => { event.preventDefault(); saveDisplayName(); });
  elements.contactForm.addEventListener("submit", async event => {
    event.preventDefault();
    const data = new FormData(elements.contactForm);
    const address = String(data.get("address")).trim().toLowerCase();
    const name = String(data.get("name")).trim();
    if (!/^[0-9a-f]{32}$/.test(address)) { showToast("Enter a valid 32-character LXMF address"); return; }
    const existing = contactFor(address);
    const contact = { name, address };
    try {
      await persistContact(contact);
      if (existing) existing.name = name;
      else state.contacts.push(contact);
      saveState(); elements.contactForm.reset(); renderContacts(); renderConversations(); showToast("Contact saved"); openConversation(address);
    } catch (error) {
      showToast(error.message);
    }
  });
  elements.composer.addEventListener("submit", event => {
    event.preventDefault();
    const content = elements.messageInput.value.trim();
    if (!content) return;
    elements.messageInput.value = "";
    elements.messageInput.style.height = "auto";
    sendMessage(content);
  });
  elements.messageInput.addEventListener("input", () => {
    elements.messageInput.style.height = "auto";
    elements.messageInput.style.height = `${Math.min(elements.messageInput.scrollHeight, 130)}px`;
  });
  elements.imageInput.addEventListener("change", () => prepareAndSendImage(elements.imageInput.files?.[0]));
  elements.qrFileInput.addEventListener("change", () => chooseQRImage(elements.qrFileInput.files?.[0]));
  elements.contactSearch.addEventListener("input", renderContacts);
  elements.scannerDialog.addEventListener("close", stopScanner);
  elements.scannerDialog.addEventListener("cancel", stopScanner);
  elements.deliveryMethod.addEventListener("change", () => { state.deliveryMethod = elements.deliveryMethod.value; saveState(); showToast("Delivery preference saved"); });
  elements.notificationPrivacy.addEventListener("change", () => { state.notifications.privacy = elements.notificationPrivacy.value; saveState(); showToast("Notification privacy saved"); });
  elements.notificationSound.addEventListener("change", () => { state.notifications.sound = elements.notificationSound.checked; if (state.notifications.sound) prepareAlertSound(); saveState(); });
  elements.notificationVibration.addEventListener("change", () => { state.notifications.vibration = elements.notificationVibration.checked; saveState(); });
  elements.configurationForm.addEventListener("input", () => {
    saveDeploymentFields();
    updateConfigurationOutput();
  });
}

function saveDeploymentFields() {
  const fields = ["bridgeHost", "bridgeListenAddress", "bridgePort", "webListenAddress", "webPort", "sharedPort", "controlPort"];
  fields.forEach(name => {
    const input = elements.configurationForm.elements.namedItem(name);
    state.deployment[name] = input.type === "number" ? Number(input.value) : input.value.trim();
  });
  saveState();
}

function generateSecret(name) {
  const input = elements.configurationForm.elements.namedItem(name);
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  input.value = [...bytes].map(byte => byte.toString(16).padStart(2, "0")).join("");
  input.type = "text";
  updateConfigurationOutput();
  showToast(name === "rpcKey" ? "RPC key generated in memory" : "Bridge token generated in memory");
}

function updateConfigurationOutput() {
  const form = elements.configurationForm.elements;
  const deployment = state.deployment;
  const bridgeToken = form.namedItem("bridgeToken").value.trim() || "BRIDGE_TOKEN";
  const rpcKey = form.namedItem("rpcKey").value.trim() || "RPC_KEY";
  elements.configurationOutput.value = `# RNSD machine: ${deployment.bridgeHost}\n# Existing rnsd configuration\n[reticulum]\n  share_instance = Yes\n  shared_instance_type = tcp\n  shared_instance_port = ${deployment.sharedPort}\n  instance_control_port = ${deployment.controlPort}\n  rpc_key = ${rpcKey}\n\n# lxmf-bridge on ${deployment.bridgeHost}\n# It shares the rnsd network namespace, so RNS remains at 127.0.0.1.\nLXMF_BRIDGE_LISTEN_ADDRESS=${deployment.bridgeListenAddress}\nLXMF_BRIDGE_PORT=${deployment.bridgePort}\nRNS_SHARED_INSTANCE_PORT=${deployment.sharedPort}\nRNS_INSTANCE_CONTROL_PORT=${deployment.controlPort}\nRNS_RPC_KEY=${rpcKey}\nLXMF_BRIDGE_TOKEN=${bridgeToken}\n\n# websideband connects to the bridge, not directly to rnsd\nLXMF_BRIDGE_URL=http://${deployment.bridgeHost}:${deployment.bridgePort}\nLXMF_BRIDGE_TOKEN=${bridgeToken}\nWEBSIDEBAND_LISTEN_ADDRESS=${deployment.webListenAddress}:${deployment.webPort}\n# Podman: --publish ${deployment.webListenAddress}:${deployment.webPort}:${deployment.webPort}`;
}

function deleteContact(address) {
  const savedContact = contactFor(address);
  if (!window.confirm(`Remove ${savedContact?.name || "this contact"}? Conversation history will be kept.`)) return;
  state.contacts = state.contacts.filter(contact => contact.address !== address);
  if (state.activePeer === address && !messagesFor(address).length) state.activePeer = null;
  saveState(); renderContacts(); renderConversations(); showToast("Contact removed");
  fetch(`/api/v1/contacts/${address}`, { method: "DELETE" }).catch(() => {});
}

function resetPreferences() {
  if (!window.confirm("Reset interface and deployment preferences on this browser? Contacts and messages will be kept.")) return;
  state.deliveryMethod = defaultState.deliveryMethod;
  state.deployment = { ...defaultDeployment };
  state.activePeer = null;
  elements.configurationForm.elements.namedItem("bridgeToken").value = "";
  elements.configurationForm.elements.namedItem("rpcKey").value = "";
  saveState();
  elements.chatContent.classList.add("hidden");
  elements.chatEmpty.classList.remove("hidden");
  elements.chatPanel.classList.remove("mobile-open");
  renderAll(); showToast("Interface preferences reset");
}

async function copyText(value, successMessage = "Address copied") {
  if (!value) return;
  try { await navigator.clipboard.writeText(value); showToast(successMessage); } catch (_) { showToast("Copy failed—select the text manually"); }
}

function showToast(message) {
  clearTimeout(toastTimer);
  elements.toast.textContent = message;
  elements.toast.classList.add("visible");
  toastTimer = setTimeout(() => elements.toast.classList.remove("visible"), 2800);
}

function avatar(text) {
  const element = document.createElement("div"); element.className = "avatar"; element.textContent = text; return element;
}

function rowCopy(title, subtitle) {
  const element = document.createElement("div"); element.className = "row-copy";
  const strong = document.createElement("strong"); strong.textContent = title;
  const span = document.createElement("span"); span.textContent = subtitle;
  element.append(strong, span); return element;
}

function meta(text) { const element = document.createElement("span"); element.className = "row-meta"; element.textContent = text; return element; }

function emptyList(title, detail) {
  const element = document.createElement("div"); element.className = "list-empty";
  const strong = document.createElement("strong"); strong.textContent = title;
  const span = document.createElement("span"); span.textContent = detail;
  element.append(strong, span); return element;
}

function relativeTime(timestamp) {
  const seconds = Math.floor((Date.now() - timestamp) / 1000);
  if (seconds < 60) return "now";
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return new Date(timestamp).toLocaleDateString([], { month: "short", day: "numeric" });
}

function renderAll() { renderConversations(); renderContacts(); renderAnnounces(); renderSettings(); if (state.activePeer) openConversation(state.activePeer); }

makeNav();
bindEvents();
navigate("conversations");
renderAll();
renderConnection(false, "Bridge unavailable");
updateOnlineState();
checkAuthentication();
if ("serviceWorker" in navigator) navigator.serviceWorker.register("/service-worker.js").catch(() => {});
window.addEventListener("online", updateOnlineState);
window.addEventListener("offline", updateOnlineState);
window.addEventListener("beforeinstallprompt", event => { event.preventDefault(); deferredInstallPrompt = event; renderInstallState(); });
window.addEventListener("appinstalled", () => { deferredInstallPrompt = null; renderInstallState(); showToast("PEREVIA installed"); });
document.addEventListener("visibilitychange", () => { if (document.visibilityState === "visible" && state.activePeer) markConversationRead(state.activePeer); });
