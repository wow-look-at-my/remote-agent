#!/bin/sh
# Auto-generated shim used by `remote-agent claude`.
#
# Claude Code invokes its shell prefix as a single-token program followed by the
# full bash script for one Bash tool call, i.e.:
#
#     /path/to/remote-agent-claude-shim.sh '<full bash script>'
#
# This shim forwards that script to the remote host through the remote-agent
# daemon, so every command Claude runs executes on the remote machine instead of
# locally. The target daemon socket is selected by remote-agent itself via the
# REMOTE_AGENT_SOCKET / REMOTE_AGENT_TARGET environment variables, which the
# launcher exports into Claude's environment.
#
# Using a single-token shim (rather than a multi-word prefix like
# "remote-agent exec") is deliberate: Claude's prefix wrapper shell-quotes the
# program name as one argument, so a multi-word prefix would be treated as a
# single executable name and fail.
exec "${REMOTE_AGENT_BIN:?REMOTE_AGENT_BIN is not set; launch via 'remote-agent claude'}" exec "$@"
