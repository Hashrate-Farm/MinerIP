# MinerIP — ASIC Miner Network Scanner

Lightweight Windows tool that scans your local network and finds all ASIC cryptocurrency miners (Antminer, Whatsminer, AvalonMiner, Bitaxe, IceRiver, Goldshell, Jasminer, VNish, and more).

**Download:** [Latest Release](https://github.com/Hashrate-Farm/MinerIP/releases/latest)

## Features

- One-click scan — automatically detects your subnet
- Identifies miner brand and model from API responses
- Displays live hashrate, temperature, fan speed, and power data
- Real-time results — miners appear as they're found
- Clean web UI opens in your default browser
- Single portable `.exe` — no installation needed
- Supports 20+ miner brands and firmware variants

## Supported Miners

| Brand | Detection Method |
|-------|-----------------|
| Bitmain Antminer | CGMiner API (4028) + Web (80/443) |
| MicroBT Whatsminer | CGMiner API (4028) + Web (80/443) |
| Canaan AvalonMiner | CGMiner API (4028) + Web (80/443) |
| Bitaxe | HTTP API (80) |
| IceRiver | Web (80/443) |
| Goldshell | Web (80/443) |
| Jasminer | Web (80/443) |
| Innosilicon | Web (80/443) |
| ePIC | Web (80/443) |
| Braiins OS+ | Web (80/443) |
| VNish Firmware | HTTP API (80) |
| Hive OS | Web (80/443) |
| LuxOS | Web (80/443) |
| And more... | Multiple ports |

## Usage

1. Download `MinerIP.zip` from the [latest release](https://github.com/privacybtc/MinerIP/releases/latest)
2. Extract and run `MinerIP.exe`
3. A browser tab opens automatically with the scanner UI
4. Click **Start Scan** — miners appear in real time

## Build from Source

```bash
# Prerequisites: Go 1.21+, go-winres
go install github.com/nickelchen/go-winres@latest
go-winres make
go build -ldflags="-s -w" -o MinerIP.exe
```

## Windows SmartScreen Notice

Because this is a new, unsigned executable, Windows SmartScreen may show a warning. Click **"More info"** → **"Run anyway"** to proceed. The source code is fully open for review.

## Solo Mining?

If you're solo mining with your ASICs, check out [**MySoloPool.com**](https://mysolopool.com) — a reliable solo mining pool supporting Bitcoin, Litecoin, Kaspa, and more. Find your miners with MinerIP, then point them to MySoloPool for the best solo mining experience.

## License

MIT

## Credits

Built by [Hashrate.Farm](https://www.hashrate.farm) | Solo mining? Try [MySoloPool.com](https://mysolopool.com)
