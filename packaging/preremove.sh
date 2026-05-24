#!/bin/sh
set -e
systemctl stop halid || true
systemctl disable halid || true
