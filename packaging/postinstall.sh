#!/bin/sh
set -e
systemctl daemon-reload
systemctl enable halid
systemctl start halid || true
