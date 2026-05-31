# Installation

`argo-compare` ships as a standalone binary and as a Docker image.

## Binary

Download the latest release from the [Releases](https://github.com/shini4i/argo-compare/releases) page for your platform and place the binary somewhere on your `PATH`.

## Docker

```bash
docker pull ghcr.io/shini4i/argo-compare:<version>
```

To run the image against your working tree:

```bash
docker run -it \
  --mount type=bind,source="$(pwd)",target=/apps \
  --env EXTERNAL_DIFF_TOOL=diff-so-fancy \
  --workdir /apps \
  ghcr.io/shini4i/argo-compare:<version> branch <target-branch> --full-output
```

The published image bundles [`kubeconform`](https://github.com/yannh/kubeconform), so [manifest validation](manifest-validation.md) works out of the box. For standalone binary installs that need validation, install `kubeconform` separately.
