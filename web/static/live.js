'use strict';

// The WebSocket feed shared by every page that shows live keg readings.
//
// liveUpdates(onMessage) opens /ws and hands each parsed frame to onMessage.
// When the socket closes, for example across a server restart, it reconnects
// after a few seconds. The server sends a fresh snapshot of every keg to each
// new connection, so a reconnected page catches up without reloading.
//
// The address is built in full because older browsers, such as the tablets
// a tap list is often left on, reject a relative WebSocket URL.
const LIVE_RETRY_MS = 5000;

function liveUpdates(onMessage) {
  const url = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/ws`;

  (function connect() {
    const ws = new WebSocket(url);
    ws.addEventListener('message', ev => {
      let msg;
      try { msg = JSON.parse(ev.data); } catch (_) { return; }
      onMessage(msg);
    });
    ws.addEventListener('close', () => setTimeout(connect, LIVE_RETRY_MS));
    ws.addEventListener('error', () => ws.close());
  })();
}
