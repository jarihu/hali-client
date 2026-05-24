# Changelog

All notable changes to hali will be documented in this file.

## [0.1.0] - Unreleased

### Added
- HF search and download for GGUF models
- Local model cache with metadata store
- Daemon with JSON-over-TCP IPC on fixed ports (47432/47433)
- BitTorrent-based LAN peer discovery and model seeding
- Web dashboard with live transfer stats
- Ollama and LM Studio model export
- Windows SCM service and system tray integration
- Linux systemd service and FHS-compliant paths
- HMAC-SHA256 signed LAN multicast
- Bearer token auth for web dashboard on non-loopback binds
- Path traversal protection via safepath validation
- Download resume support
- Streaming piece hash verification
- MIT license
