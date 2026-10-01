# dr-demo-app

The "production" service for the *GitHub Actions → Tekton* DR demo
(control plane: [github-actions-dr-demo](https://github.com/agarwalvivek29/github-actions-dr-demo)).

Both release paths run the same scripts:

| step   | GitHub Actions (`.github/workflows/release.yml`) | Tekton (`release` Pipeline) |
|--------|--------------------------------------------------|-----------------------------|
| build  | `./ci/build.sh`                                  | `./ci/build.sh`             |
| deploy | `./ci/deploy.sh` (`DEPLOYED_BY=github-actions`)  | `./ci/deploy.sh` (`DEPLOYED_BY=tekton`) |

To ship a "hotfix", edit `message` in `main.go` and push to `main`.
