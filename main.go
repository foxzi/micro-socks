package main

import (
    "bufio"
    "encoding/binary"
    "errors"
    "flag"
    "fmt"
    "io"
    "log"
    "net"
    "os"
    "os/signal"
    "strings"
    "sync"
    "syscall"
    "time"
)

// SOCKS5 protocol constants
const (
    VERSION            byte = 0x05
    CMD_CONNECT       byte = 0x01
    ATYP_IPV4         byte = 0x01
    ATYP_DOMAINNAME   byte = 0x03
    ATYP_IPV6         byte = 0x04
    
    // Authentication methods
    AUTH_NONE         byte = 0x00
    AUTH_USERNAME     byte = 0x02
    AUTH_NOACCEPT     byte = 0xFF
    
    // Reply codes
    REP_SUCCESS              byte = 0x00
    REP_GENERAL_FAILURE      byte = 0x01
    REP_NOT_ALLOWED          byte = 0x02
    REP_NETWORK_UNREACHABLE  byte = 0x03
    REP_HOST_UNREACHABLE     byte = 0x04
    REP_CONNECTION_REFUSED   byte = 0x05
    REP_TTL_EXPIRED          byte = 0x06
    REP_CMD_NOT_SUPPORTED    byte = 0x07
    REP_ATYP_NOT_SUPPORTED   byte = 0x08
)

// Configuration
type Config struct {
    ListenAddr    string
    OutboundIface string
    Users         map[string]string
}

func main() {
    var config Config
    
    // Parse command line flags
    listenAddr := flag.String("listen", "0.0.0.0:1080", "Listen address and port")
    listenAddrShort := flag.String("l", "0.0.0.0:1080", "Listen address and port (short)")
    outIface := flag.String("iface", "", "Outbound network interface")
    outIfaceShort := flag.String("i", "", "Outbound network interface (short)")
    userFile := flag.String("users", "", "User file (format: username:password)")
    userFileShort := flag.String("u", "", "User file (short)")
    flag.Parse()

    // Merge short and long flags (short takes precedence if both set)
    if *listenAddrShort != "0.0.0.0:1080" {
        listenAddr = listenAddrShort
    }
    if *outIfaceShort != "" {
        outIface = outIfaceShort
    }
    if *userFileShort != "" {
        userFile = userFileShort
    }
    
    // Env overrides if flags left at defaults
    if v := os.Getenv("PROXY_LISTEN"); v != "" && *listenAddr == "0.0.0.0:1080" {
        config.ListenAddr = v
    } else {
        config.ListenAddr = *listenAddr
    }
    if v := os.Getenv("PROXY_IFACE"); v != "" && *outIface == "" {
        config.OutboundIface = v
    } else {
        config.OutboundIface = *outIface
    }
    config.Users = make(map[string]string)
    
    // Load users from file
    usersPath := *userFile
    if usersPath == "" {
        usersPath = os.Getenv("PROXY_USERS")
    }
    if usersPath != "" {
        if err := loadUsers(usersPath, &config); err != nil {
            log.Fatalf("Error loading users: %v", err)
        }
    }
    
    // Check if authentication is required
    requireAuth := len(config.Users) > 0
    
    // Start the server
    listener, err := net.Listen("tcp", config.ListenAddr)
    if err != nil {
        log.Fatalf("Failed to start server: %v", err)
    }
    defer listener.Close()

    // Signal handling for graceful shutdown
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    go func() {
        <-sigCh
        log.Printf("Shutting down, closing listener...")
        listener.Close()
    }()
    
    log.Printf("SOCKS5 proxy started on %s", config.ListenAddr)
    if config.OutboundIface != "" {
        if ip := getInterfaceIP(config.OutboundIface); ip != nil {
            log.Printf("Outbound traffic through interface: %s (%s)", config.OutboundIface, ip.String())
        } else {
            log.Printf("Warning: interface %s not found or no IPv4 address; using default routing", config.OutboundIface)
        }
    }
    if requireAuth {
        log.Printf("Authentication enabled, loaded %d users", len(config.Users))
    } else {
        log.Printf("Authentication disabled")
    }
    
    for {
        conn, err := listener.Accept()
        if err != nil {
            // net.ErrClosed -> shutdown
            var ne net.Error
            if errors.Is(err, net.ErrClosed) || (errors.As(err, &ne) && !ne.Timeout()) {
                break
            }
            log.Printf("Accept error: %v", err)
            continue
        }
        go handleConnection(conn, &config)
    }
}

// Load users from file
func loadUsers(filename string, config *Config) error {
    f, err := os.Open(filename)
    if err != nil {
        if os.IsNotExist(err) {
            return fmt.Errorf("user file not found: %s\nPlease create the file with format: username:password (one per line)", filename)
        }
        if os.IsPermission(err) {
            return fmt.Errorf("permission denied reading user file: %s\nCheck file permissions", filename)
        }
        return fmt.Errorf("failed to open user file %s: %w", filename, err)
    }
    defer f.Close()

    scanner := bufio.NewScanner(f)
    for scanner.Scan() {
        line := strings.TrimSpace(scanner.Text())
        if line == "" || strings.HasPrefix(line, "#") {
            continue
        }
        parts := strings.SplitN(line, ":", 2)
        if len(parts) != 2 {
            continue
        }
        user := strings.TrimSpace(parts[0])
        pass := strings.TrimSpace(parts[1])
        if user != "" && pass != "" {
            config.Users[user] = pass
        }
    }
    return scanner.Err()
}

// Handle client connection
func handleConnection(conn net.Conn, config *Config) {
    defer conn.Close()
    // Enable TCP keep-alive
    if tc, ok := conn.(*net.TCPConn); ok {
        tc.SetKeepAlive(true)
        tc.SetKeepAlivePeriod(30 * time.Second)
    }
    
    // Deadlines for handshake
    conn.SetDeadline(time.Now().Add(15 * time.Second))
    // Authentication negotiation
    if err := negotiateAuth(conn, config); err != nil {
	log.Printf("Authentication error: %v", err)
	return
    }
    
    // Process SOCKS request
    if err := handleRequest(conn, config); err != nil {
	log.Printf("Request handling error: %v", err)
	return
    }
}

// Negotiate authentication methods
func negotiateAuth(conn net.Conn, config *Config) error {
    // First packet with authentication methods
    header := make([]byte, 2)
    if _, err := io.ReadFull(conn, header); err != nil {
	return err
    }
    
    if header[0] != VERSION {
	return errors.New("invalid protocol version")
    }
    
    methodCount := int(header[1])
    methods := make([]byte, methodCount)
    if _, err := io.ReadFull(conn, methods); err != nil {
	return err
    }
    
    // Check for required authentication method
    requireAuth := len(config.Users) > 0
    chosenMethod := AUTH_NOACCEPT
    
    for _, method := range methods {
	if requireAuth && method == AUTH_USERNAME {
	    chosenMethod = AUTH_USERNAME
	    break
	} else if !requireAuth && method == AUTH_NONE {
	    chosenMethod = AUTH_NONE
	    break
	}
    }
    
    // Send chosen method
    if _, err := conn.Write([]byte{VERSION, chosenMethod}); err != nil {
	return err
    }
    
    // If method is not acceptable, close connection
    if chosenMethod == AUTH_NOACCEPT {
	return errors.New("no supported authentication methods")
    }
    
    // Verify credentials if authentication required
    if chosenMethod == AUTH_USERNAME {
	auth := make([]byte, 2)
	if _, err := io.ReadFull(conn, auth); err != nil {
	    return err
	}
	
	if auth[0] != 0x01 {
	    return errors.New("invalid authentication subprotocol version")
	}
	
	// Read username
	usernameLen := int(auth[1])
	username := make([]byte, usernameLen)
	if _, err := io.ReadFull(conn, username); err != nil {
	    return err
	}
	
	// Read password
	passwordLenBuf := make([]byte, 1)
	if _, err := io.ReadFull(conn, passwordLenBuf); err != nil {
	    return err
	}
	
	passwordLen := int(passwordLenBuf[0])
	password := make([]byte, passwordLen)
	if _, err := io.ReadFull(conn, password); err != nil {
	    return err
	}
	
	// Verify credentials
	usernameStr := string(username)
	passwordStr := string(password)
	
	storedPassword, exists := config.Users[usernameStr]
	if !exists || storedPassword != passwordStr {
	    // Send authentication status: failure
	    conn.Write([]byte{0x01, 0x01})
	    return errors.New("invalid username or password")
	}
	
	// Send authentication status: success
	if _, err := conn.Write([]byte{0x01, 0x00}); err != nil {
	    return err
	}
    }
    
    return nil
}

// Handle SOCKS request
func handleRequest(conn net.Conn, config *Config) error {
    // Read request header
    header := make([]byte, 4)
    if _, err := io.ReadFull(conn, header); err != nil {
	return err
    }
    
    if header[0] != VERSION {
	return errors.New("invalid protocol version")
    }
    
    if header[1] != CMD_CONNECT {
        writeReply(conn, REP_CMD_NOT_SUPPORTED, nil, 0)
        return errors.New("only CONNECT command is supported")
    }
    
    // Read and process destination address
    var dstAddr string
    var dstPort uint16
    
    switch header[3] {
    case ATYP_IPV4:
	// IPv4
	ipv4 := make([]byte, 4)
	if _, err := io.ReadFull(conn, ipv4); err != nil {
	    return err
	}
	dstAddr = net.IPv4(ipv4[0], ipv4[1], ipv4[2], ipv4[3]).String()
	
    case ATYP_DOMAINNAME:
	// Domain name
	domainLenBuff := make([]byte, 1)
	if _, err := io.ReadFull(conn, domainLenBuff); err != nil {
	    return err
	}
	domainLen := int(domainLenBuff[0])
	
	domain := make([]byte, domainLen)
	if _, err := io.ReadFull(conn, domain); err != nil {
	    return err
	}
	dstAddr = string(domain)
	
    case ATYP_IPV6:
	// IPv6
	ipv6 := make([]byte, 16)
	if _, err := io.ReadFull(conn, ipv6); err != nil {
	    return err
	}
	dstAddr = net.IP(ipv6).String()
	
    default:
        writeReply(conn, REP_ATYP_NOT_SUPPORTED, nil, 0)
        return errors.New("unsupported address type")
    }
    
    // Read port
    portBuff := make([]byte, 2)
    if _, err := io.ReadFull(conn, portBuff); err != nil {
	return err
    }
    dstPort = binary.BigEndian.Uint16(portBuff)
    
    // Create connection to target host
    var targetConn net.Conn
    var err error
    
    targetAddr := fmt.Sprintf("%s:%d", dstAddr, dstPort)
    log.Printf("Connecting to %s", targetAddr)
    
    dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
    if config.OutboundIface != "" {
        if ip := getInterfaceIP(config.OutboundIface); ip != nil {
            dialer.LocalAddr = &net.TCPAddr{IP: ip}
        }
    }
    targetConn, err = dialer.Dial("tcp", targetAddr)
    
    if err != nil {
        log.Printf("Failed to connect to %s: %v", targetAddr, err)
        writeReply(conn, mapDialError(err), nil, 0)
        return err
    }
    defer targetConn.Close()
    
    // Send success response
    localAddr := targetConn.LocalAddr().(*net.TCPAddr)
    ipBytes := localAddr.IP.To4()
    if ipBytes == nil {
	ipBytes = localAddr.IP.To16()
    }
    
    if err := writeBoundSuccess(conn, ipBytes, uint16(localAddr.Port)); err != nil {
        return err
    }
    
    // Clear deadlines for long-lived proxying
    conn.SetDeadline(time.Time{})
    if tc, ok := targetConn.(*net.TCPConn); ok {
        tc.SetKeepAlive(true)
        tc.SetKeepAlivePeriod(30 * time.Second)
    }

    // Forward data between connections
    var wg sync.WaitGroup
    wg.Add(2)
    
    // Client -> Server
    go func() {
        defer wg.Done()
        buf := getBuf()
        io.CopyBuffer(targetConn, conn, buf)
        putBuf(buf)
        if tc, ok := targetConn.(*net.TCPConn); ok {
            tc.CloseWrite()
        }
    }()
    
    // Server -> Client
    go func() {
        defer wg.Done()
        buf := getBuf()
        io.CopyBuffer(conn, targetConn, buf)
        putBuf(buf)
        if tc, ok := conn.(*net.TCPConn); ok {
            tc.CloseWrite()
        }
    }()
    
    wg.Wait()
    return nil
}

// Get IP address of specified interface
func getInterfaceIP(ifaceName string) net.IP {
    iface, err := net.InterfaceByName(ifaceName)
    if err != nil {
	log.Printf("Error getting interface %s: %v", ifaceName, err)
	return nil
    }
    
    addrs, err := iface.Addrs()
    if err != nil {
	log.Printf("Error getting addresses for interface %s: %v", ifaceName, err)
	return nil
    }
    
    for _, addr := range addrs {
	switch v := addr.(type) {
	case *net.IPNet:
	    if !v.IP.IsLoopback() {
		if v.IP.To4() != nil {
		    return v.IP
		}
	    }
	}
    }
    
    return nil
}

// Helper: write a generic reply with optional bind addr/port
func writeReply(conn net.Conn, rep byte, bindIP net.IP, bindPort uint16) error {
    addrType := ATYP_IPV4
    addrBytes := []byte{0, 0, 0, 0}
    if bindIP != nil {
        if v4 := bindIP.To4(); v4 != nil {
            addrType = ATYP_IPV4
            addrBytes = v4
        } else if v6 := bindIP.To16(); v6 != nil {
            addrType = ATYP_IPV6
            addrBytes = v6
        }
    }
    reply := []byte{VERSION, rep, 0x00, addrType}
    reply = append(reply, addrBytes...)
    portBytes := make([]byte, 2)
    binary.BigEndian.PutUint16(portBytes, bindPort)
    reply = append(reply, portBytes...)
    _, err := conn.Write(reply)
    return err
}

// Helper: success reply with BND addr/port
func writeBoundSuccess(conn net.Conn, ipBytes []byte, port uint16) error {
    addrType := ATYP_IPV4
    if len(ipBytes) == 16 {
        addrType = ATYP_IPV6
    }
    reply := []byte{VERSION, REP_SUCCESS, 0x00, addrType}
    reply = append(reply, ipBytes...)
    portBytes := make([]byte, 2)
    binary.BigEndian.PutUint16(portBytes, port)
    reply = append(reply, portBytes...)
    _, err := conn.Write(reply)
    return err
}

// Map dialing errors to SOCKS5 reply codes as best effort
func mapDialError(err error) byte {
    // Timeout or temporary errors
    if ne, ok := err.(net.Error); ok && ne.Timeout() {
        return REP_HOST_UNREACHABLE
    }
    // Connection refused
    var se *os.SyscallError
    if errors.As(err, &se) && se.Err == syscall.ECONNREFUSED {
        return REP_CONNECTION_REFUSED
    }
    // DNS errors
    var de *net.DNSError
    if errors.As(err, &de) {
        return REP_HOST_UNREACHABLE
    }
    s := err.Error()
    switch {
    case strings.Contains(s, "connection refused"):
        return REP_CONNECTION_REFUSED
    case strings.Contains(s, "network is unreachable"):
        return REP_NETWORK_UNREACHABLE
    }
    return REP_GENERAL_FAILURE
}

// Buffer pool for proxying
var bufPool = sync.Pool{New: func() any { b := make([]byte, 32*1024); return b }}

func getBuf() []byte  { return bufPool.Get().([]byte) }
func putBuf(b []byte) { bufPool.Put(b) }
