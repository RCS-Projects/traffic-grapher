# Traffic Grapher — Current Project Notes

Traffic Grapher is a Linux-only Docker service for live SNMP bandwidth monitoring. It serves a browser dashboard on port `8080`, with draggable interface/group graphs and a rolling 15-minute history.

## Deployment

The supported deployment is Docker Compose:

```bash
docker compose up -d --build
```

The service uses host networking so it can poll LAN SNMP devices directly and is reachable at `http://<server-ip>:8080`. Configuration is persisted in `data/config.json` next to this Compose project.

## Technology

- Go HTTP/WebSocket server and SNMP polling (`gosnmp`)
- Vanilla JavaScript and `uPlot` frontend
- Multi-stage Docker build: Node builds the frontend, Go builds the server, Alpine runs the final image

## Operations

```bash
docker compose ps
docker compose logs -f
docker compose restart
docker compose down
```

No host executable, Windows package, systemd unit, or nohup launcher is maintained.
