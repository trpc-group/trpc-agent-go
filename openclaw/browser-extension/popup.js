const serverURL = document.getElementById("server-url");
const status = document.getElementById("status");

function request(message) {
  return chrome.runtime.sendMessage(message);
}

async function refresh() {
  const result = await request({ type: "status" });
  if (!result.ok) {
    status.textContent = result.error || "Unknown error";
    return;
  }
  serverURL.value = result.serverURL || "";
  const tabs = (result.attachedTabs || [])
    .map((tab) => `${tab.targetId} ${tab.title} ${tab.url}`)
    .join("\n");
  status.textContent = [
    `Client: ${result.clientId}`,
    `Connected: ${result.connected}`,
    tabs ? `Attached:\n${tabs}` : "Attached: none"
  ].join("\n");
}

document.getElementById("save").addEventListener("click", async () => {
  await request({
    type: "set_server_url",
    serverURL: serverURL.value
  });
  await refresh();
});

document.getElementById("attach").addEventListener("click", async () => {
  await request({ type: "attach_current_tab" });
  await refresh();
});

document.getElementById("detach").addEventListener("click", async () => {
  await request({ type: "detach_current_tab" });
  await refresh();
});

refresh().catch((error) => {
  status.textContent = `${error.message || error}`;
});
