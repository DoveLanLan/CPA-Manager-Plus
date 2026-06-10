# Production Deployment

This directory contains the VPS deployment assets for `cpa-manager-plus`.

The GitHub Actions deploy workflow uploads these files to `/opt/cpa-manager-plus` and runs
`scripts/remote-deploy.sh` after the GHCR image build succeeds on `main`.

The default server layout intentionally preserves the existing production data directory:

```text
/opt/cpa-manager-plus/
  .env
  compose.production.yml
  scripts/remote-deploy.sh

/opt/cliproxyapi/data/cpa-manager/
  usage.sqlite
  data.key
  admin-key.txt
  cpa-management-key.txt
```

Default private access:

- `http://100.67.99.9:18318/management.html#/`

Required GitHub production environment secrets:

- `PRODUCTION_SSH_PRIVATE_KEY`
- `PRODUCTION_SSH_KNOWN_HOSTS`

The deployment reuses the existing `vps-gateway` Docker network so the service can reach
`cli-proxy-api` at `http://cli-proxy-api:8317`.
