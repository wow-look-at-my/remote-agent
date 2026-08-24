package daemon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// defaultSSHPort is the port a target with no port of its own uses, unless
// ssh_config names another one for the host.
const defaultSSHPort = 22

// Endpoint is a parsed SSH target: the login, the host and the port.
type Endpoint struct {
	User string // empty when the target names no user
	Host string // host name, IP address, or ~/.ssh/config Host alias
	Port int    // 0 when the target names no port
}

// ParseTarget splits a target of the form [user@]host[:port]. An IPv6 address
// with a port needs brackets ([::1]:2222). An address with more than one colon
// and no brackets is a host with no port.
func ParseTarget(target string) (Endpoint, error) {
	rest := strings.TrimSpace(target)
	if rest == "" {
		return Endpoint{}, fmt.Errorf("empty SSH target")
	}

	var ep Endpoint
	if user, host, found := strings.Cut(rest, "@"); found {
		ep.User, rest = user, host
	}

	port := ""
	switch {
	case strings.HasPrefix(rest, "["):
		end := strings.Index(rest, "]")
		if end < 0 {
			return Endpoint{}, fmt.Errorf("invalid SSH target %q: no closing bracket", target)
		}
		ep.Host, rest = rest[1:end], rest[end+1:]
		if rest != "" {
			if !strings.HasPrefix(rest, ":") {
				return Endpoint{}, fmt.Errorf("invalid SSH target %q: expected :port after the address", target)
			}
			port = rest[1:]
		}
	case strings.Count(rest, ":") == 1:
		ep.Host, port, _ = strings.Cut(rest, ":")
	default:
		ep.Host = rest
	}

	if ep.Host == "" {
		return Endpoint{}, fmt.Errorf("invalid SSH target %q: no host", target)
	}
	// A colon with nothing after it is a typed port that got lost. Reading it
	// as "no port" would send the connection to port 22 without a word.
	if strings.HasSuffix(rest, ":") {
		return Endpoint{}, fmt.Errorf("invalid SSH target %q: no port after the colon", target)
	}
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return Endpoint{}, fmt.Errorf("invalid SSH target %q: bad port %q", target, port)
		}
		ep.Port = n
	}
	return ep, nil
}

// String renders an endpoint back as [user@]host[:port].
func (e Endpoint) String() string {
	host := e.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if e.User != "" {
		host = e.User + "@" + host
	}
	if e.Port != 0 {
		host += ":" + strconv.Itoa(e.Port)
	}
	return host
}

// Login returns the user a connection to this endpoint logs in as: the user in
// the target, else $USER, else root.
func (e Endpoint) Login() string {
	if e.User != "" {
		return e.User
	}
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "root"
}

// CanonicalTarget returns the string a daemon keys on. The port belongs to
// that identity: one host on two ports is two machines, and a daemon keyed on
// user@host alone answers for whichever port connected first. A port given
// separately merges into the target. Two different ports for one target are an
// error, because the alternative is a silent choice between two hosts.
func CanonicalTarget(target string, port int) (string, error) {
	ep, err := ParseTarget(target)
	if err != nil {
		return "", err
	}
	if port > 0 {
		if ep.Port > 0 && ep.Port != port {
			return "", fmt.Errorf("target %s names port %d, but port %d was given as well", target, ep.Port, port)
		}
		ep.Port = port
	}
	return ep.String(), nil
}

// normalizeTarget is CanonicalTarget for the path helpers, which have no way
// to report an error: a target that does not parse keys on its own text.
func normalizeTarget(target string) string {
	ep, err := ParseTarget(target)
	if err != nil {
		return target
	}
	return ep.String()
}
