#!/bin/sh
set -e

REPO="alansikora/clanopy"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

TAG=""
while [ $# -gt 0 ]; do
  case "$1" in
    --canary)  TAG="canary"; shift ;;
    --version) TAG="$2"; shift 2 ;;
    *)         echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

detect_os() {
  case "$(uname -s)" in
    Linux)  echo "linux" ;;
    Darwin) echo "darwin" ;;
    *)      echo "Unsupported OS: $(uname -s)" >&2; exit 1 ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)   echo "amd64" ;;
    arm64|aarch64)   echo "arm64" ;;
    *)               echo "Unsupported architecture: $(uname -m)" >&2; exit 1 ;;
  esac
}

OS="$(detect_os)"
ARCH="$(detect_arch)"

if [ -z "$TAG" ]; then
  echo "Fetching latest release..."
  TAG="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' \
    | cut -d'"' -f4)"
fi

echo "Fetching release ${TAG}..."
URL="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/tags/${TAG}" \
  | grep '"browser_download_url"' \
  | grep "_${OS}_${ARCH}\.tar\.gz" \
  | cut -d'"' -f4)"

if [ -z "$URL" ]; then
  echo "Error: could not find asset for ${OS}/${ARCH} in release ${TAG}" >&2
  exit 1
fi

ARCHIVE="${URL##*/}"
_v="${ARCHIVE%.tar.gz}"
_v="${_v#clanopy_}"
VERSION="${_v%_${OS}_${ARCH}}"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

echo "Downloading clanopy ${VERSION} for ${OS}/${ARCH}..."
curl -fsSL -o "${TMPDIR}/${ARCHIVE}" "${URL}"

tar -xzf "${TMPDIR}/${ARCHIVE}" -C "${TMPDIR}"

mkdir -p "${INSTALL_DIR}"
cp "${TMPDIR}/clanopy" "${INSTALL_DIR}/clanopy"
chmod +x "${INSTALL_DIR}/clanopy"

echo "clanopy ${VERSION} installed to ${INSTALL_DIR}/clanopy"
echo ""

case ":${PATH}:" in
  *:"${INSTALL_DIR}":*) ;;
  *)
    echo "Warning: ${INSTALL_DIR} is not in your PATH."
    echo "Add this to your shell rc file:"
    echo ""
    echo "  export PATH=\"${INSTALL_DIR}:\$PATH\""
    echo ""
    ;;
esac

echo "Get started:"
echo "  clanopy init zsh   # or bash, fish"
echo "  clanopy"
