package sshclient

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHClient represents an SSH client connection
type SSHClient struct {
	client     *ssh.Client
	config     *ssh.ClientConfig
	host       string
	port       string
	persistent bool
	session    *ssh.Session // Persistent session for continuous commands
}

// NewSSHClient creates a new SSH client
func NewSSHClient(username, privateKeyPath, host, port string) (*SSHClient, error) {
	// Read private key
	key, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read private key: %v", err)
	}

	// Parse private key
	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("unable to parse private key: %v", err)
	}

	// SSH client config
	config := &ssh.ClientConfig{
		User: username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // For development - should be improved for production
	}

	return &SSHClient{
		config:     config,
		host:       host,
		port:       port,
		persistent: false,
	}, nil
}

// NewPersistentSSHClient creates a new SSH client with persistent connection
func NewPersistentSSHClient(username, privateKeyPath, host, port string) (*SSHClient, error) {
	client, err := NewSSHClient(username, privateKeyPath, host, port)
	if err != nil {
		return nil, err
	}
	client.persistent = true
	return client, nil
}

// Connect establishes the SSH connection
func (c *SSHClient) Connect() error {
	addr := fmt.Sprintf("%s:%s", c.host, c.port)
	client, err := ssh.Dial("tcp", addr, c.config)
	if err != nil {
		return fmt.Errorf("failed to dial: %v", err)
	}
	c.client = client
	return nil
}

// Close closes the SSH connection
func (c *SSHClient) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// UploadFile uploads a local file to remote server
func (c *SSHClient) UploadFile(localPath, remotePath string) error {

	// Open local file
	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %v", err)
	}
	defer localFile.Close()

	// Get file info for size
	stat, err := localFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat local file: %v", err)
	}

	// Create remote file
	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	// Create remote directory if it doesn't exist
	remoteDir := filepath.Dir(remotePath)
	mkdirCmd := fmt.Sprintf("mkdir -p %s", remoteDir)
	if err := c.RunCommand(mkdirCmd); err != nil {
		return fmt.Errorf("failed to create remote directory: %v", err)
	}

	// Use scp protocol properly: run scp -t on remote directory and drive protocol over stdin/stdout

	// Prepare pipes
	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stdin pipe: %v", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stdout pipe: %v", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stderr pipe: %v", err)
	}

	// Start scp in 'to' mode targeting the remote directory (not including filename)
	targetDir := filepath.Dir(remotePath)
	if err := session.Start(fmt.Sprintf("scp -t %s", targetDir)); err != nil {
		session.Close()
		return fmt.Errorf("failed to start scp on remote: %v", err)
	}

	// helper to read a single-byte ACK from remote with timeout to avoid blocking forever
	readAck := func() error {
		buf := make([]byte, 1)
		ch := make(chan error, 1)

		go func() {
			if _, err := stdout.Read(buf); err != nil {
				ch <- fmt.Errorf("failed to read scp ack: %v", err)
				return
			}
			switch buf[0] {
			case 0:
				ch <- nil
			case 1, 2:
				// read error message from stderr
				msg := make([]byte, 1024)
				n, _ := stderr.Read(msg)
				ch <- fmt.Errorf("scp remote error: %s", strings.TrimSpace(string(msg[:n])))
			default:
				ch <- fmt.Errorf("unknown scp ack: %v", buf[0])
			}
		}()

		select {
		case err := <-ch:
			return err
		case <-time.After(10 * time.Second):
			return fmt.Errorf("timeout waiting for scp ack")
		}
	}

	// initial ack
	if err := readAck(); err != nil {
		stdin.Close()
		session.Wait()
		return err
	}

	// send file header: C<mode> <size> <filename>\n
	filename := filepath.Base(remotePath)

	fmt.Fprintf(stdin, "C%04o %d %s\n", stat.Mode().Perm(), stat.Size(), filename)

	if err := readAck(); err != nil {
		stdin.Close()
		session.Wait()
		return err
	}

	// send file data
	if _, err := io.Copy(stdin, localFile); err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("failed to send file data: %v", err)
	}

	// send trailing null byte
	if _, err := fmt.Fprint(stdin, "\x00"); err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("failed to send scp terminator: %v", err)
	}

	if err := readAck(); err != nil {
		stdin.Close()
		session.Wait()
		return err
	}

	// close stdin to indicate EOF to remote scp
	stdin.Close()

	// wait for remote scp to finish
	if err := session.Wait(); err != nil {
		return fmt.Errorf("remote scp command failed: %v", err)
	}

	return nil
}

// RunCommand executes a command on the remote server
func (c *SSHClient) RunCommand(cmd string) error {
	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	return session.Run(cmd)
}

// RunCommandWithOutput executes a command on the remote server and returns output
func (c *SSHClient) RunCommandWithOutput(cmd string) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	output, err := session.Output(cmd)
	if err != nil {
		return "", fmt.Errorf("command failed: %v", err)
	}

	return string(output), nil
}

// SyncFile syncs a single file from local to remote
func (c *SSHClient) SyncFile(localPath, remotePath string) error {
	return c.UploadFile(localPath, remotePath)
}

// DownloadFile downloads a remote file to localPath using scp -f protocol
func (c *SSHClient) DownloadFile(localPath, remotePath string) error {
	if c.client == nil {
		return fmt.Errorf("SSH client not connected")
	}

	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %v", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stdin pipe: %v", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stdout pipe: %v", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		session.Close()
		return fmt.Errorf("failed to get stderr pipe: %v", err)
	}

	// Start scp in 'from' mode to fetch the file
	if err := session.Start(fmt.Sprintf("scp -f %s", remotePath)); err != nil {
		session.Close()
		return fmt.Errorf("failed to start scp on remote: %v", err)
	}

	// helper to write a single null byte to request/ack
	writeNull := func() error {
		if _, err := stdin.Write([]byte{0}); err != nil {
			return fmt.Errorf("failed to write scp null byte: %v", err)
		}
		return nil
	}

	// send initial null to start transfer
	if err := writeNull(); err != nil {
		stdin.Close()
		session.Wait()
		return err
	}

	reader := bufio.NewReader(stdout)

	// read header byte
	b, err := reader.ReadByte()
	if err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("failed to read scp header byte: %v", err)
	}

	if b == 1 || b == 2 {
		// error; read message from stderr
		msg := make([]byte, 1024)
		n, _ := stderr.Read(msg)
		stdin.Close()
		session.Wait()
		return fmt.Errorf("scp remote error: %s", strings.TrimSpace(string(msg[:n])))
	}

	if b != 'C' {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("unexpected scp header: %v", b)
	}

	// read header line until newline
	headerLine, err := reader.ReadString('\n')
	if err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("failed to read scp header line: %v", err)
	}

	// header format: <mode> <size> <filename>\n
	parts := strings.Fields(strings.TrimSpace(headerLine))
	if len(parts) < 3 {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("invalid scp header: %s", headerLine)
	}

	// parse size
	size64, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("invalid size in scp header: %v", err)
	}

	// create local directory if needed
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("failed to create local directories: %v", err)
	}

	// open local file for writing
	lf, err := os.Create(localPath)
	if err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("failed to create local file: %v", err)
	}
	defer lf.Close()

	// send null to indicate ready to receive data
	if err := writeNull(); err != nil {
		stdin.Close()
		session.Wait()
		return err
	}

	// copy exactly size bytes from reader to local file
	if _, err := io.CopyN(lf, reader, size64); err != nil {
		stdin.Close()
		session.Wait()
		return fmt.Errorf("failed to copy file data: %v", err)
	}

	// read the trailing byte (should be a null)
	if ack, err := reader.ReadByte(); err != nil || ack != 0 {
		// try read error message
		msg := make([]byte, 1024)
		n, _ := stderr.Read(msg)
		stdin.Close()
		session.Wait()
		if err != nil {
			return fmt.Errorf("failed after data copy: %v", err)
		}
		return fmt.Errorf("scp did not acknowledge data: %s", strings.TrimSpace(string(msg[:n])))
	}

	// send final null
	if err := writeNull(); err != nil {
		stdin.Close()
		session.Wait()
		return err
	}

	stdin.Close()
	if err := session.Wait(); err != nil {
		return fmt.Errorf("remote scp command failed: %v", err)
	}

	return nil
}

// StartPersistentSession starts a persistent SSH session for continuous commands
func (c *SSHClient) StartPersistentSession() error {
	if !c.persistent {
		return fmt.Errorf("client not configured for persistent connection")
	}

	if c.client == nil {
		return fmt.Errorf("SSH client not connected")
	}

	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create persistent session: %v", err)
	}

	c.session = session
	return nil
}

// StopPersistentSession stops the persistent session
func (c *SSHClient) StopPersistentSession() error {
	if c.session != nil {
		err := c.session.Close()
		c.session = nil
		return err
	}
	return nil
}

// RunCommandPersistent executes a command using the persistent session
func (c *SSHClient) RunCommandPersistent(cmd string) error {
	if c.session == nil {
		return fmt.Errorf("persistent session not started")
	}

	return c.session.Run(cmd)
}

// RunCommandWithStream executes a command and returns channels for output and errors
func (c *SSHClient) RunCommandWithStream(cmd string) (<-chan string, <-chan error, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create session: %v", err)
	}

	// Request a pty so remote process is attached to the session's terminal
	// This helps ensure the remote process receives SIGHUP when the session closes.
	// Requesting a pty is best-effort; ignore error if not supported.
	if err := session.RequestPty("xterm", 80, 40, ssh.TerminalModes{}); err != nil {
		// non-fatal
	}

	// Get stdout pipe for streaming output
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, nil, fmt.Errorf("failed to get stdout pipe: %v", err)
	}

	// Get stderr pipe for error handling
	stderr, err := session.StderrPipe()
	if err != nil {
		session.Close()
		return nil, nil, fmt.Errorf("failed to get stderr pipe: %v", err)
	}

	// Create channels for output and errors
	outputChan := make(chan string, 100)
	errorChan := make(chan error, 10)

	// Start the command
	if err := session.Start(cmd); err != nil {
		session.Close()
		return nil, nil, fmt.Errorf("failed to start command: %v", err)
	}

	// Handle stdout in a goroutine
	go func() {
		defer close(outputChan)
		buf := make([]byte, 1024)
		for {
			n, err := stdout.Read(buf)
			if err != nil {
				if err != io.EOF {
					// inform caller
					select {
					case errorChan <- err:
					default:
					}
					// close session to ensure remote process is signaled
					session.Close()
				}
				break
			}
			if n > 0 {
				output := string(buf[:n])
				select {
				case outputChan <- output:
				default:
					// Channel full, skip to prevent blocking
				}
			}
		}
	}()

	// Handle stderr in a goroutine
	go func() {
		defer close(errorChan)
		buf := make([]byte, 1024)
		for {
			n, err := stderr.Read(buf)
			if err != nil {
				if err != io.EOF {
					select {
					case errorChan <- err:
					default:
					}
					// close session to ensure remote process is signaled
					session.Close()
				}
				break
			}
			if n > 0 {
				errorMsg := string(buf[:n])
				select {
				case errorChan <- fmt.Errorf("stderr: %s", errorMsg):
				default:
				}
			}
		}
	}()

	// Handle session completion in a goroutine
	go func() {
		err := session.Wait()
		session.Close()
		if err != nil {
			errorChan <- fmt.Errorf("command completed with error: %v", err)
		}
	}()

	return outputChan, errorChan, nil
}

// IsPersistent returns whether this client uses persistent connections
func (c *SSHClient) IsPersistent() bool {
	return c.persistent
}

// GetSession returns the current persistent session (for advanced usage)
func (c *SSHClient) GetSession() *ssh.Session {
	return c.session
}
