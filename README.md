# Traffic Grapher

Traffic Grapher is a self-hosted, Linux-first SNMP bandwidth dashboard. It runs as one Docker Compose service, scans SNMPv1/v2c devices, and shows a live, configurable graph for each selected interface or group.

It is intended for a local network: no cloud account, database server, or separate web server is required.

## What it does

- Live inbound/outbound traffic graphs with a rolling 5-minute view
- SNMPv1 and SNMPv2c device scanning
- Named ports, draggable/reorderable and resizable graph cards
- One to four dashboard columns, saved per browser/dashboard configuration
- Interface groups: each group includes a bright combined total and uniquely colored member traces
- A small browser PWA and a Linux Firefox kiosk launcher
- Persistent configuration in a local Docker volume directory

## Quick start

### 1. Install Docker on a Linux host

Install Docker Engine and the Docker Compose plugin using your distribution's Docker instructions. Confirm both are available:

```bash
docker --version
docker compose version
```

### 2. Clone and start Traffic Grapher

```bash
git clone https://github.com/RCS-Projects/traffic-grapher.git
cd traffic-grapher
docker compose up -d --build
```

The first build downloads the Go and Node build images. Later starts only need:

```bash
docker compose up -d
```

### 3. Open the dashboard

Browse to:

```text
http://YOUR-SERVER-IP:8080
```

For example, if the Docker host is `192.168.1.50`, open `http://192.168.1.50:8080`.

Open the floating gear icon, scan an SNMP device, select its interfaces, then start monitoring. The dashboard is intentionally graph-first; all setup controls stay in the side drawer.

## Docker deployment details

The included [`compose.yaml`](compose.yaml) is the recommended deployment method. It uses Linux host networking so the container can reach SNMP devices on the same LAN and serves the dashboard directly on port `8080`.

Configuration survives image upgrades in `./data/config.json`, including devices, interface names, groups, dashboard order, sizes, and graph-column setting. To back it up:

```bash
cp data/config.json traffic-grapher-config-backup.json
```

To update a checkout after pulling new source:

```bash
git pull
docker compose up -d --build
```

Useful operational commands:

```bash
docker compose ps
docker compose logs -f traffic-grapher
docker compose restart
docker compose down
```

`docker compose down` stops the service but preserves `./data`. Delete `./data` only if you intentionally want a completely empty configuration.

## Using the dashboard

- **Port names:** Open Settings and choose **Rename** next to an interface. Leave the prompt empty to return to the SNMP name.
- **Groups:** Select the member interfaces first, then add a group. A group chart shows the combined total plus colored traces for each member; the per-port cards remain available as well.
- **Layout:** In Settings → Dashboard cards, choose one to four graphs across. Drag a graph by its handle to reorder it and drag its lower-right handle to set its height.
- **Live updates:** Samples update the charts in place, so resizing, dragging, and the current zoom/cursor interaction are not reset by each SNMP poll.
- **Polling rate:** Change the interval in Settings. Many SNMP agents are comfortable at 1–3 seconds, but use a longer interval for slow or low-end hardware.

## App-style browser window

- **Chromium, Chrome, Edge:** Use the browser's **Install app** action. It opens Traffic Grapher in its own compact window.
- **Firefox on Linux:** Firefox desktop does not currently install web manifests as standalone apps. Copy the included launcher, update its server address if needed, then launch **Traffic Graphs** from your application menu:

  ```bash
  mkdir -p ~/.local/share/applications
  cp desktop/traffic-graphs-firefox.desktop ~/.local/share/applications/
  ```

  The launcher uses Firefox kiosk mode. Press `Alt+F4` to close it.

## Troubleshooting

- **A scan reports `GETBULK not supported in SNMPv1`:** Scan the device as SNMPv1; Traffic Grapher automatically uses the compatible scan method.
- **A scan reports `connection refused`:** The target is refusing UDP SNMP on that IP/port. Check that SNMP is enabled, the community is correct, and UDP/161 (or your selected port) is reachable.
- **No traffic appears immediately:** The first poll reads counters only; the second poll calculates the rate. Wait one polling interval after monitoring starts.
- **Cannot reach the web page:** Verify `docker compose ps`, then check the host firewall permits TCP port 8080.

## Development

The production image builds both the Go backend and browser bundle. For a full local image build:

```bash
docker compose build
```

If Go and Node are installed on the host, the quick checks are:

```bash
go test ./...
npm run build --prefix frontend
```
