# The daemon's identity is the target, and the port is part of it.
#
# No test here reaches an SSH host. Two things are observable without one: the
# socket path a command picks for a target, and the address the CLI dials. Both
# are what the identity decides, so both are what these tests assert.

sandbox:
	network: false

shared:
	files:
		socket-for.sh: |
			# Print the daemon socket path the CLI picks for the target in $1.
			# The "no daemon running" error names that socket, and
			# REMOTE_AGENT_NO_AUTOSTART keeps the lookup from starting one.
			REMOTE_AGENT_NO_AUTOSTART=1 "$GO_TOOLCHAIN_DATS_BUILD_DIR/remote-agent" --target "$1" ping 2>&1 |
				sed -n 's/.*connect to daemon at \([^ ]*\.sock\).*/\1/p'

tests:
	- desc: two ports on one host are two daemons
	  cmd: a=$(sh {shared.socket-for.sh} root@127.0.0.1:2201); b=$(sh {shared.socket-for.sh} root@127.0.0.1:2202); test -n "$a"; test "$a" != "$b"; echo separate-sockets
	  outputs:
		stdout:
			- separate-sockets

	- desc: one endpoint keys one daemon, whichever way the port arrives
	  cmd: a=$(sh {shared.socket-for.sh} root@127.0.0.1:2201); b=$(REMOTE_AGENT_PORT=2201 sh {shared.socket-for.sh} root@127.0.0.1); test -n "$a"; test "$a" = "$b"; echo same-socket
	  outputs:
		stdout:
			- same-socket

	- desc: a target that names no port is an endpoint of its own
	  cmd: a=$(sh {shared.socket-for.sh} root@127.0.0.1); b=$(sh {shared.socket-for.sh} root@127.0.0.1:2201); test -n "$a"; test "$a" != "$b"; echo separate-sockets
	  outputs:
		stdout:
			- separate-sockets

	- desc: two ports that disagree are refused, not guessed
	  cmd: RA=$GO_TOOLCHAIN_DATS_BUILD_DIR/remote-agent; REMOTE_AGENT_PORT=2202 "$RA" --target root@127.0.0.1:2201 ping
	  exit: 1
	  outputs:
		stderr:
			- names port 2201
			- port 2202 was given as well

	- desc: a port that is not a number is refused
	  cmd: RA=$GO_TOOLCHAIN_DATS_BUILD_DIR/remote-agent; "$RA" --target root@127.0.0.1:ssh ping
	  exit: 1
	  outputs:
		stderr:
			- 'bad port "ssh"'

	- desc: connect names the port from the target, not port 22
	  cmd: RA=$GO_TOOLCHAIN_DATS_BUILD_DIR/remote-agent; "$RA" connect root@127.0.0.1:47823
	  exit: 1
	  outputs:
		stderr:
			- "Connecting to root@127.0.0.1:47823..."
		!stderr:
			- "127.0.0.1:22"

	- desc: connect brackets an IPv6 address
	  cmd: RA=$GO_TOOLCHAIN_DATS_BUILD_DIR/remote-agent; "$RA" connect 'root@[::1]:47823'
	  exit: 1
	  outputs:
		stderr:
			- "Connecting to root@[::1]:47823..."

	- desc: the MCP default target keeps its port
	  cmd: RA=$GO_TOOLCHAIN_DATS_BUILD_DIR/remote-agent; "$RA" mcp root@127.0.0.1:2201
	  inputs:
		stdin: '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
	  outputs:
		stdout:
			- Defaults to root@127.0.0.1:2201.

	- desc: the MCP target argument documents the port form
	  cmd: RA=$GO_TOOLCHAIN_DATS_BUILD_DIR/remote-agent; "$RA" mcp root@127.0.0.1:2201
	  inputs:
		stdin: '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
	  outputs:
		stdout:
			- user@host:2222 for a non-standard SSH port

	# A model cannot restart its own MCP server, so the deadline on a command has
	# to be an argument of the call. see docs/daemon/timeouts.md
	- desc: run_command takes a deadline per call
	  cmd: RA=$GO_TOOLCHAIN_DATS_BUILD_DIR/remote-agent; "$RA" mcp root@127.0.0.1:2201
	  inputs:
		stdin: '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
	  outputs:
		stdout:
			- Seconds to wait before giving up on the command
			- the command may still be running on the remote host

	- desc: a deadline that is not a number is refused rather than ignored
	  cmd: RA=$GO_TOOLCHAIN_DATS_BUILD_DIR/remote-agent; REMOTE_AGENT_TIMEOUT=soon "$RA" --target root@127.0.0.1:47823 exec echo hi
	  exit: 1
	  outputs:
		stderr:
			- REMOTE_AGENT_TIMEOUT="soon" is not a positive number of seconds
