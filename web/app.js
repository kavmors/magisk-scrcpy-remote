const displayCanvas = document.querySelector("#displayCanvas");
const screenCtx = displayCanvas.getContext("2d", { alpha: false });
const statusEl = document.querySelector("#status");
const form = document.querySelector("#connectForm");
const tokenInput = document.querySelector("#token");
const connectBtn = document.querySelector("#connect");
const disconnectBtn = document.querySelector("#disconnect");
const textInput = document.querySelector("#textInput");
const screenshotBtn = document.querySelector("#screenshot");
const recordBtn = document.querySelector("#record");
const toolScreens = document.querySelectorAll(".tool-screen");
const openToolButtons = document.querySelectorAll("[data-tool-open]");
const closeToolButtons = document.querySelectorAll("[data-tool-close]");
const apkFileInput = document.querySelector("#apkFile");
const installApkBtn = document.querySelector("#installApk");
const apkOutput = document.querySelector("#apkOutput");
const filePathInput = document.querySelector("#filePath");
const fileRefreshBtn = document.querySelector("#fileRefresh");
const fileParentBtn = document.querySelector("#fileParent");
const fileUploadInput = document.querySelector("#fileUpload");
const uploadFileBtn = document.querySelector("#uploadFile");
const fileList = document.querySelector("#fileList");
const fileOutput = document.querySelector("#fileOutput");
const shellForm = document.querySelector("#shellForm");
const shellCommandInput = document.querySelector("#shellCommand");
const connectShellBtn = document.querySelector("#connectShell");
const disconnectShellBtn = document.querySelector("#disconnectShell");
const clearShellBtn = document.querySelector("#clearShell");
const runShellBtn = document.querySelector("#runShell");
const shellOutput = document.querySelector("#shellOutput");
const shellState = document.querySelector("#shellState");

const STREAM_VIDEO = 1;
const STREAM_AUDIO = 2;
const FLAG_CONFIG = 1;
const FLAG_KEYFRAME = 2;
const SCREEN_FIT_PADDING = 16;
const SCREEN_MAX_SCALE = 0.96;
const TOKEN_STORAGE_KEY = "msr.lastConnectedToken";

let ws;
let videoDecoder;
let audioDecoder;
let audioContext;
let audioNextTime = 0;
let pointerDown = false;
let pendingVideoConfig;
let lastVideoTimestamp = 0;
let screenWidth = 1;
let screenHeight = 1;
let resizePending = false;
let mediaRecorder;
let recordedChunks = [];
let recordStartedAt = "";
let currentFileParent = "";
let filesLoaded = false;
let shellWS;
let pendingShellCommands = [];

restoreSavedToken();
setStatus("未连接");
redirectInsecureLANToHTTPS();
window.addEventListener("resize", scheduleScreenFit);

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  await connect();
});

disconnectBtn.addEventListener("click", () => disconnect());
screenshotBtn.addEventListener("click", () => saveScreenshot());
recordBtn.addEventListener("click", () => toggleRecording());
openToolButtons.forEach((button) => {
  button.addEventListener("click", () => openTool(button.dataset.toolOpen));
});
closeToolButtons.forEach((button) => {
  button.addEventListener("click", () => closeTool());
});
installApkBtn.addEventListener("click", () => installAPK());
fileRefreshBtn.addEventListener("click", () => loadFiles(filePathInput.value));
fileParentBtn.addEventListener("click", () => {
  if (currentFileParent) loadFiles(currentFileParent);
});
uploadFileBtn.addEventListener("click", () => uploadFile());
shellForm.addEventListener("submit", (event) => {
  event.preventDefault();
  sendShellCommand();
});
connectShellBtn.addEventListener("click", () => startShellSession());
disconnectShellBtn.addEventListener("click", () => stopShellSession());
clearShellBtn.addEventListener("click", () => {
  shellOutput.textContent = "";
});

document.querySelectorAll("[data-command='back']").forEach((button) => {
  button.addEventListener("click", () => sendBack());
});

document.querySelectorAll("[data-keycode]").forEach((button) => {
  button.addEventListener("click", () => {
    const androidKeyCode = Number(button.dataset.keycode);
    send({ type: "keycode", action: "down", androidKeyCode });
    send({ type: "keycode", action: "up", androidKeyCode });
  });
});

textInput.addEventListener("keydown", (event) => {
  if (event.key === "Enter" && textInput.value) {
    send({ type: "text", text: textInput.value });
    textInput.value = "";
    event.preventDefault();
  }
});

displayCanvas.addEventListener("pointerdown", (event) => {
  if (event.button === 2) {
    sendBack();
    event.preventDefault();
    return;
  }
  pointerDown = true;
  displayCanvas.setPointerCapture(event.pointerId);
  sendPointer("down", event);
});

displayCanvas.addEventListener("contextmenu", (event) => {
  event.preventDefault();
});

displayCanvas.addEventListener("pointermove", (event) => {
  if (pointerDown) sendPointer("move", event);
});

displayCanvas.addEventListener("pointerup", (event) => {
  pointerDown = false;
  sendPointer("up", event);
});

displayCanvas.addEventListener("pointercancel", (event) => {
  pointerDown = false;
  sendPointer("up", event);
});

displayCanvas.addEventListener("wheel", (event) => {
  const point = normalizedPoint(event);
  send({
    type: "scroll",
    x: point.x,
    y: point.y,
    deltaX: event.deltaX,
    deltaY: event.deltaY,
  });
  event.preventDefault();
}, { passive: false });

window.addEventListener("keydown", (event) => {
  if (activeTool()) {
    if (event.key === "Escape") {
      closeTool();
      event.preventDefault();
    }
    return;
  }
  if (!isConnected() || event.target === tokenInput || event.target === textInput) return;
  const androidKeyCode = keyToAndroid(event);
  if (!androidKeyCode) return;
  send({ type: "keycode", action: "down", androidKeyCode });
  event.preventDefault();
});

window.addEventListener("keyup", (event) => {
  if (activeTool()) return;
  if (!isConnected() || event.target === tokenInput || event.target === textInput) return;
  const androidKeyCode = keyToAndroid(event);
  if (!androidKeyCode) return;
  send({ type: "keycode", action: "up", androidKeyCode });
  event.preventDefault();
});

async function connect() {
  disconnect();

  const supportError = webCodecsSupportError();
  if (supportError) {
    setStatus(supportError);
    console.warn(supportError, {
      isSecureContext,
      hasVideoDecoder: "VideoDecoder" in window,
      hasEncodedVideoChunk: "EncodedVideoChunk" in window,
      userAgent: navigator.userAgent,
    });
    return;
  }

  setStatus("连接中");
  connectBtn.disabled = true;

  audioContext = new AudioContext({ sampleRate: 48000 });
  await audioContext.resume();
  audioNextTime = audioContext.currentTime + 0.08;

  initVideoDecoder();
  initAudioDecoder();

  const scheme = location.protocol === "https:" ? "wss" : "ws";
  const token = encodeURIComponent(tokenInput.value.trim());
  ws = new WebSocket(`${scheme}://${location.host}/ws-stream?token=${token}`);
  ws.binaryType = "arraybuffer";
  ws.onopen = () => {
    rememberConnectedToken(tokenInput.value.trim());
    setStatus("已连接");
    disconnectBtn.disabled = false;
  };
  ws.onclose = () => {
    setStatus("已断开");
    connectBtn.disabled = false;
    disconnectBtn.disabled = true;
    closeDecoders();
  };
  ws.onerror = () => setStatus("连接失败");
  ws.onmessage = (event) => {
    if (typeof event.data === "string") {
      handleServerMessage(JSON.parse(event.data));
      return;
    }
    handleMediaFrame(event.data);
  };
}

function disconnect() {
  if (ws) ws.close();
  ws = undefined;
  closeDecoders();
  stopRecording();
  connectBtn.disabled = false;
  disconnectBtn.disabled = true;
}

function webCodecsSupportError() {
  if (!isSecureContext) {
    return "请使用 HTTPS 或 localhost 打开页面";
  }
  if (!("VideoDecoder" in window)) {
    return "当前浏览器没有 VideoDecoder";
  }
  if (!("EncodedVideoChunk" in window)) {
    return "当前浏览器没有 EncodedVideoChunk";
  }
  return "";
}

function redirectInsecureLANToHTTPS() {
  if (location.protocol !== "http:" || isLoopbackHost(location.hostname)) return;
  setStatus("正在切换到 HTTPS");
  location.replace(`https://${location.host}${location.pathname}${location.search}${location.hash}`);
}

function isLoopbackHost(hostname) {
  return hostname === "localhost" || hostname === "127.0.0.1" || hostname === "::1" || hostname === "[::1]";
}

function restoreSavedToken() {
  try {
    const token = localStorage.getItem(TOKEN_STORAGE_KEY);
    if (token) tokenInput.value = token;
  } catch (error) {
    console.warn("restore token failed", error);
  }
}

function rememberConnectedToken(token) {
  try {
    if (token) {
      localStorage.setItem(TOKEN_STORAGE_KEY, token);
    } else {
      localStorage.removeItem(TOKEN_STORAGE_KEY);
    }
  } catch (error) {
    console.warn("save token failed", error);
  }
}

function initVideoDecoder(codec = "avc1.42E01F") {
  closeVideoDecoder();
  videoDecoder = new VideoDecoder({
    output: (frame) => {
      drawFrame(frame);
      frame.close();
    },
    error: (error) => {
      console.error(error);
      setStatus("视频解码失败");
    },
  });
  videoDecoder.configure({
    codec,
    optimizeForLatency: true,
  });
}

function initAudioDecoder() {
  if (!("AudioDecoder" in window) || !("EncodedAudioChunk" in window)) return;
  audioDecoder = new AudioDecoder({
    output: (audioData) => {
      playAudio(audioData);
      audioData.close();
    },
    error: (error) => console.warn("audio decode failed", error),
  });
  audioDecoder.configure({
    codec: "opus",
    sampleRate: 48000,
    numberOfChannels: 2,
  });
}

function closeDecoders() {
  closeVideoDecoder();
  if (audioDecoder) {
    audioDecoder.close();
    audioDecoder = undefined;
  }
  if (audioContext) {
    audioContext.close();
    audioContext = undefined;
  }
}

function closeVideoDecoder() {
  if (videoDecoder) {
    videoDecoder.close();
    videoDecoder = undefined;
  }
}

function handleServerMessage(message) {
  if (message.type === "video-session") {
    screenWidth = message.width || 1;
    screenHeight = message.height || 1;
    displayCanvas.width = screenWidth;
    displayCanvas.height = screenHeight;
    fitScreenToStage();
  } else if (message.type === "audio-disabled") {
    console.warn("audio disabled by device");
  }
}

function handleMediaFrame(buffer) {
  const data = new Uint8Array(buffer);
  if (data.length < 10) return;

  const stream = data[0];
  const flags = data[1];
  const timestamp = Number(new DataView(data.buffer, data.byteOffset + 2, 8).getBigUint64(0));
  const payload = data.slice(10);

  if (stream === STREAM_VIDEO) {
    handleVideoPacket(flags, timestamp, payload);
  } else if (stream === STREAM_AUDIO) {
    handleAudioPacket(timestamp, payload);
  }
}

function handleVideoPacket(flags, timestamp, payload) {
  if (flags & FLAG_CONFIG) {
    pendingVideoConfig = payload;
    const codec = codecFromAnnexB(payload);
    if (codec) initVideoDecoder(codec);
    return;
  }

  let chunkData = payload;
  if (pendingVideoConfig) {
    chunkData = concatBytes(pendingVideoConfig, payload);
    pendingVideoConfig = undefined;
  }

  if (!videoDecoder || videoDecoder.state !== "configured") return;

  const type = flags & FLAG_KEYFRAME ? "key" : "delta";
  const duration = lastVideoTimestamp && timestamp > lastVideoTimestamp
    ? timestamp - lastVideoTimestamp
    : 33333;
  lastVideoTimestamp = timestamp;

  try {
    videoDecoder.decode(new EncodedVideoChunk({
      type,
      timestamp,
      duration,
      data: chunkData,
    }));
  } catch (error) {
    console.error(error);
    setStatus("视频包处理失败");
  }
}

function handleAudioPacket(timestamp, payload) {
  if (!audioDecoder || audioDecoder.state !== "configured") return;
  try {
    audioDecoder.decode(new EncodedAudioChunk({
      type: "key",
      timestamp,
      duration: 20000,
      data: payload,
    }));
  } catch (error) {
    console.warn("audio packet failed", error);
  }
}

function drawFrame(frame) {
  if (displayCanvas.width !== frame.displayWidth || displayCanvas.height !== frame.displayHeight) {
    displayCanvas.width = frame.displayWidth;
    displayCanvas.height = frame.displayHeight;
    screenWidth = frame.displayWidth;
    screenHeight = frame.displayHeight;
    fitScreenToStage();
  }
  screenCtx.drawImage(frame, 0, 0, displayCanvas.width, displayCanvas.height);
}

function playAudio(audioData) {
  if (!audioContext) return;

  const channels = audioData.numberOfChannels;
  const frames = audioData.numberOfFrames;
  const sampleRate = audioData.sampleRate;
  const audioBuffer = audioContext.createBuffer(channels, frames, sampleRate);

  for (let channel = 0; channel < channels; channel += 1) {
    audioData.copyTo(audioBuffer.getChannelData(channel), {
      planeIndex: channel,
      format: "f32-planar",
    });
  }

  const source = audioContext.createBufferSource();
  source.buffer = audioBuffer;
  source.connect(audioContext.destination);
  const startAt = Math.max(audioContext.currentTime, audioNextTime);
  source.start(startAt);
  audioNextTime = startAt + audioBuffer.duration;
}

function sendBack() {
  send({ type: "back" });
}

function saveScreenshot() {
  if (!displayCanvas.width || !displayCanvas.height) return;
  const filename = `screenshot_${datetimeStamp()}.png`;
  displayCanvas.toBlob((blob) => {
    if (!blob) return;
    downloadBlob(blob, filename);
  }, "image/png");
}

function toggleRecording() {
  if (mediaRecorder && mediaRecorder.state === "recording") {
    stopRecording();
    return;
  }
  startRecording();
}

function startRecording() {
  if (!displayCanvas.captureStream || typeof MediaRecorder === "undefined") {
    setStatus("当前浏览器不支持录屏");
    return;
  }
  const stream = displayCanvas.captureStream(30);
  const options = preferredRecorderOptions();
  recordedChunks = [];
  recordStartedAt = datetimeStamp();
  mediaRecorder = new MediaRecorder(stream, options);
  mediaRecorder.addEventListener("dataavailable", (event) => {
    if (event.data && event.data.size > 0) recordedChunks.push(event.data);
  });
  mediaRecorder.addEventListener("stop", () => {
    const mimeType = mediaRecorder.mimeType || options.mimeType || "video/webm";
    const blob = new Blob(recordedChunks, { type: mimeType });
    recordedChunks = [];
    stream.getTracks().forEach((track) => track.stop());
    mediaRecorder = undefined;
    recordBtn.textContent = "录屏";
    recordBtn.classList.remove("recording");
    if (blob.size > 0) downloadBlob(blob, `screenrecord_${recordStartedAt}.webm`);
    recordStartedAt = "";
  });
  mediaRecorder.start(1000);
  recordBtn.textContent = "停止录屏";
  recordBtn.classList.add("recording");
}

function stopRecording() {
  if (mediaRecorder && mediaRecorder.state === "recording") {
    mediaRecorder.stop();
  }
}

function preferredRecorderOptions() {
  const candidates = [
    "video/webm;codecs=vp9",
    "video/webm;codecs=vp8",
    "video/webm",
  ];
  for (const mimeType of candidates) {
    if (MediaRecorder.isTypeSupported(mimeType)) return { mimeType, videoBitsPerSecond: 6000000 };
  }
  return { videoBitsPerSecond: 6000000 };
}

function datetimeStamp() {
  const d = new Date();
  const pad = (value) => String(value).padStart(2, "0");
  return [
    d.getFullYear(),
    pad(d.getMonth() + 1),
    pad(d.getDate()),
    "_",
    pad(d.getHours()),
    pad(d.getMinutes()),
    pad(d.getSeconds()),
  ].join("");
}

function downloadBlob(blob, filename) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function openTool(id) {
  closeTool();
  const screen = document.querySelector(`#${id}`);
  if (!screen) return;
  screen.classList.add("open");
  screen.setAttribute("aria-hidden", "false");
  const closeButton = screen.querySelector("[data-tool-close]");
  if (closeButton) closeButton.focus();
  if (id === "filesTool" && !filesLoaded) {
    filesLoaded = true;
    loadFiles(filePathInput.value);
  }
  if (id === "shellTool") {
    startShellSession();
  }
}

function closeTool() {
  const screen = activeTool();
  if (!screen) return;
  screen.classList.remove("open");
  screen.setAttribute("aria-hidden", "true");
}

function activeTool() {
  return document.querySelector(".tool-screen.open");
}

async function installAPK() {
  const file = apkFileInput.files[0];
  if (!file) {
    apkOutput.textContent = "请选择 APK 文件";
    return;
  }
  installApkBtn.disabled = true;
  apkOutput.textContent = "安装中...";
  try {
    const body = new FormData();
    body.append("apk", file);
    const result = await apiJSON("/api/install-apk", { method: "POST", body });
    apkOutput.textContent = formatResult(result);
  } catch (error) {
    apkOutput.textContent = String(error.message || error);
  } finally {
    installApkBtn.disabled = false;
  }
}

async function loadFiles(path = "/sdcard") {
  fileRefreshBtn.disabled = true;
  fileOutput.textContent = "读取中...";
  try {
    const data = await apiJSON(`/api/files?path=${encodeURIComponent(path || "/sdcard")}`);
    filePathInput.value = data.path || path;
    currentFileParent = data.parent || "";
    renderFileList(data.entries || []);
    fileOutput.textContent = `${data.entries?.length || 0} 项`;
  } catch (error) {
    fileOutput.textContent = String(error.message || error);
  } finally {
    fileRefreshBtn.disabled = false;
  }
}

function renderFileList(entries) {
  fileList.replaceChildren();
  if (!entries.length) {
    const empty = document.createElement("div");
    empty.className = "file-entry";
    empty.textContent = "空目录";
    fileList.appendChild(empty);
    return;
  }
  for (const entry of entries) {
    const row = document.createElement("div");
    row.className = "file-entry";

    const name = document.createElement("button");
    name.type = "button";
    name.className = "file-name";
    name.textContent = `${entry.isDir ? "目录 " : "文件 "}${entry.name}`;
    name.addEventListener("click", () => {
      if (entry.isDir) {
        loadFiles(entry.path);
      } else {
        downloadFile(entry);
      }
    });

    const meta = document.createElement("span");
    meta.className = "file-meta";
    meta.textContent = entry.isDir ? entry.mode : `${formatBytes(entry.size)} ${entry.mode}`;

    const action = document.createElement("button");
    action.type = "button";
    action.textContent = entry.isDir ? "打开" : "下载";
    action.addEventListener("click", () => {
      if (entry.isDir) {
        loadFiles(entry.path);
      } else {
        downloadFile(entry);
      }
    });

    row.append(name, meta, action);
    fileList.appendChild(row);
  }
}

async function uploadFile() {
  const file = fileUploadInput.files[0];
  if (!file) {
    fileOutput.textContent = "请选择要上传的文件";
    return;
  }
  uploadFileBtn.disabled = true;
  fileOutput.textContent = "上传中...";
  try {
    const body = new FormData();
    body.append("file", file);
    const result = await apiJSON(`/api/files/upload?path=${encodeURIComponent(filePathInput.value || "/sdcard/Download")}`, {
      method: "POST",
      body,
    });
    fileOutput.textContent = formatResult(result);
    await loadFiles(filePathInput.value);
  } catch (error) {
    fileOutput.textContent = String(error.message || error);
  } finally {
    uploadFileBtn.disabled = false;
  }
}

async function downloadFile(entry) {
  fileOutput.textContent = "下载中...";
  try {
    const response = await apiFetch(`/api/files/download?path=${encodeURIComponent(entry.path)}`);
    const blob = await response.blob();
    downloadBlob(blob, entry.name);
    fileOutput.textContent = `已下载 ${entry.path}`;
  } catch (error) {
    fileOutput.textContent = String(error.message || error);
  }
}

function startShellSession() {
  if (shellWS && (shellWS.readyState === WebSocket.OPEN || shellWS.readyState === WebSocket.CONNECTING)) return;
  const scheme = location.protocol === "https:" ? "wss" : "ws";
  const token = encodeURIComponent(tokenInput.value.trim());
  shellWS = new WebSocket(`${scheme}://${location.host}/api/shell-stream?token=${token}`);
  setShellState("连接中");
  shellWS.onopen = () => {
    setShellState("已连接");
    flushPendingShellCommands();
  };
  shellWS.onmessage = (event) => {
    appendShellOutput(String(event.data));
  };
  shellWS.onerror = () => {
    setShellState("连接失败");
  };
  shellWS.onclose = () => {
    shellWS = undefined;
    setShellState("已断开");
  };
}

function stopShellSession() {
  if (shellWS) shellWS.close();
  shellWS = undefined;
  pendingShellCommands = [];
  setShellState("已断开");
}

function sendShellCommand() {
  const command = shellCommandInput.value.trim();
  if (!command) {
    return;
  }
  shellCommandInput.value = "";
  appendShellOutput(`$ ${command}\n`);
  if (!shellWS || shellWS.readyState !== WebSocket.OPEN) {
    pendingShellCommands.push(command);
    startShellSession();
    return;
  }
  shellWS.send(`${command}\n`);
}

function flushPendingShellCommands() {
  if (!shellWS || shellWS.readyState !== WebSocket.OPEN) return;
  for (const command of pendingShellCommands) {
    shellWS.send(`${command}\n`);
  }
  pendingShellCommands = [];
}

function appendShellOutput(text) {
  shellOutput.textContent += text;
  shellOutput.scrollTop = shellOutput.scrollHeight;
}

function setShellState(text) {
  shellState.textContent = text;
  const connected = text === "已连接";
  connectShellBtn.disabled = connected || text === "连接中";
  disconnectShellBtn.disabled = !shellWS;
  runShellBtn.disabled = text === "连接中";
}

async function apiJSON(path, options = {}) {
  const response = await apiFetch(path, options);
  return response.json();
}

async function apiFetch(path, options = {}) {
  const headers = new Headers(options.headers || {});
  const token = tokenInput.value.trim();
  if (token) headers.set("X-MSR-Token", token);
  const response = await fetch(path, { ...options, headers });
  if (!response.ok) {
    const text = await response.text();
    throw new Error(text || `${response.status} ${response.statusText}`);
  }
  return response;
}

function formatResult(result) {
  return JSON.stringify(result, null, 2);
}

function formatBytes(size) {
  if (!Number.isFinite(size)) return "";
  const units = ["B", "KB", "MB", "GB"];
  let value = size;
  let index = 0;
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024;
    index += 1;
  }
  return `${value.toFixed(index ? 1 : 0)} ${units[index]}`;
}

function sendPointer(action, event) {
  const point = normalizedPoint(event);
  send({ type: "touch", action, x: point.x, y: point.y });
  event.preventDefault();
}

function normalizedPoint(event) {
  const rect = screenContentRect();
  return {
    x: clamp01((event.clientX - rect.left) / rect.width),
    y: clamp01((event.clientY - rect.top) / rect.height),
  };
}

function screenContentRect() {
  return displayCanvas.getBoundingClientRect();
}

function fitScreenToStage() {
  const stage = displayCanvas.parentElement;
  const stageStyle = getComputedStyle(stage);
  const horizontalPadding = parseFloat(stageStyle.paddingLeft) + parseFloat(stageStyle.paddingRight);
  const verticalPadding = parseFloat(stageStyle.paddingTop) + parseFloat(stageStyle.paddingBottom);
  const availableWidth = stage.clientWidth - horizontalPadding - SCREEN_FIT_PADDING * 2;
  const availableHeight = stage.clientHeight - verticalPadding - SCREEN_FIT_PADDING * 2;
  if (!screenWidth || !screenHeight || availableWidth <= 0 || availableHeight <= 0) return;

  const scale = Math.min(
    availableWidth / screenWidth,
    availableHeight / screenHeight,
    SCREEN_MAX_SCALE,
  );
  const cssWidth = Math.max(1, Math.floor(screenWidth * scale));
  const cssHeight = Math.max(1, Math.floor(screenHeight * scale));

  displayCanvas.style.width = `${cssWidth}px`;
  displayCanvas.style.height = `${cssHeight}px`;
}

function scheduleScreenFit() {
  if (resizePending) return;
  resizePending = true;
  requestAnimationFrame(() => {
    resizePending = false;
    fitScreenToStage();
  });
}

function send(data) {
  if (isConnected()) {
    ws.send(JSON.stringify(data));
  }
}

function isConnected() {
  return ws && ws.readyState === WebSocket.OPEN;
}

function concatBytes(a, b) {
  const out = new Uint8Array(a.length + b.length);
  out.set(a, 0);
  out.set(b, a.length);
  return out;
}

function codecFromAnnexB(data) {
  const units = annexBNalUnits(data);
  for (const unit of units) {
    if ((unit[0] & 0x1f) === 7 && unit.length >= 4) {
      return `avc1.${hex2(unit[1])}${hex2(unit[2])}${hex2(unit[3])}`;
    }
  }
  return "";
}

function annexBNalUnits(data) {
  const units = [];
  let start = findStartCode(data, 0);
  while (start >= 0) {
    const nalStart = start + startCodeLength(data, start);
    const next = findStartCode(data, nalStart);
    const nalEnd = next >= 0 ? next : data.length;
    if (nalEnd > nalStart) units.push(data.slice(nalStart, nalEnd));
    start = next;
  }
  return units;
}

function findStartCode(data, offset) {
  for (let i = offset; i + 3 < data.length; i += 1) {
    if (data[i] === 0 && data[i + 1] === 0 && data[i + 2] === 1) return i;
    if (i + 4 < data.length && data[i] === 0 && data[i + 1] === 0 && data[i + 2] === 0 && data[i + 3] === 1) return i;
  }
  return -1;
}

function startCodeLength(data, index) {
  return data[index + 2] === 1 ? 3 : 4;
}

function hex2(value) {
  return value.toString(16).padStart(2, "0").toUpperCase();
}

function clamp01(value) {
  if (value < 0) return 0;
  if (value > 1) return 1;
  return value;
}

function keyToAndroid(event) {
  if (event.key.length === 1) {
    const c = event.key.toUpperCase();
    if (c >= "A" && c <= "Z") return c.charCodeAt(0) - 36;
    if (c >= "0" && c <= "9") return c === "0" ? 7 : c.charCodeAt(0) - 41;
  }
  const map = {
    Enter: 66,
    Backspace: 67,
    Tab: 61,
    Escape: 4,
    ArrowUp: 19,
    ArrowDown: 20,
    ArrowLeft: 21,
    ArrowRight: 22,
    Delete: 112,
    " ": 62,
  };
  return map[event.key] || 0;
}

function setStatus(text) {
  statusEl.textContent = text;
}
