const displayCanvas = document.querySelector("#displayCanvas");
const screenCtx = displayCanvas.getContext("2d", { alpha: false });
const statusEl = document.querySelector("#status");
const form = document.querySelector("#connectForm");
const tokenInput = document.querySelector("#token");
const connectBtn = document.querySelector("#connect");
const disconnectBtn = document.querySelector("#disconnect");
const textInput = document.querySelector("#textInput");

const STREAM_VIDEO = 1;
const STREAM_AUDIO = 2;
const FLAG_CONFIG = 1;
const FLAG_KEYFRAME = 2;
const SCREEN_FIT_PADDING = 16;
const SCREEN_MAX_SCALE = 0.96;

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

setStatus("未连接");
redirectInsecureLANToHTTPS();
window.addEventListener("resize", scheduleScreenFit);

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  await connect();
});

disconnectBtn.addEventListener("click", () => disconnect());

document.querySelectorAll("[data-command='back']").forEach((button) => {
  button.addEventListener("click", () => send({ type: "back" }));
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
  pointerDown = true;
  displayCanvas.setPointerCapture(event.pointerId);
  sendPointer("down", event);
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
  if (!isConnected() || event.target === tokenInput || event.target === textInput) return;
  const androidKeyCode = keyToAndroid(event);
  if (!androidKeyCode) return;
  send({ type: "keycode", action: "down", androidKeyCode });
  event.preventDefault();
});

window.addEventListener("keyup", (event) => {
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
