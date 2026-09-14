# Add go and shared go tools to the PATH.
# Do NOT export GOPATH: go has defaulted it to $HOME/go since 1.8, and an exported
# GOPATH leaks through su/daemons to less privileged users (see go-env-review.md).
# Root installs shared tools for everyone with: GOBIN=/usr/local/gopath/bin go install
export PATH=$PATH:/usr/local/go/bin:/usr/local/gopath/bin
if [ -n "$HOME" ]; then
  export PATH=$PATH:$HOME/go/bin
fi
