import os, pty, select, signal, struct, sys, time
import re

binary = os.path.abspath(sys.argv[1])

def start():
    pid, fd = pty.fork()
    if pid == 0:
        os.execv(binary, [binary])
    fcntl = __import__('fcntl')
    termios = __import__('termios')
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack('HHHH', 30, 110, 0, 0))
    return pid, fd

def read_until(pid, fd, text, output, timeout=40):
    deadline = time.monotonic() + timeout
    while text.encode() not in re.sub(rb'\x1b\[[0-?]*[ -/]*[@-~]', b'', output):
        if time.monotonic() > deadline:
            try: os.kill(pid, signal.SIGKILL)
            except ProcessLookupError: pass
            try: os.waitpid(pid, 0)
            except ChildProcessError: pass
            raise AssertionError(f'timed out waiting for {text!r}: {output[-2500:]!r}')
        ready, _, _ = select.select([fd], [], [], 0.1)
        if ready:
            try: chunk = os.read(fd, 8192)
            except OSError: break
            if not chunk: break
            output.extend(chunk)
        done, status = os.waitpid(pid, os.WNOHANG)
        if done: raise AssertionError(f'process exited before {text!r}: {status}, {output[-1500:]!r}')

def finish(pid, fd, output, timeout=40):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        ready, _, _ = select.select([fd], [], [], 0.1)
        if ready:
            try: output.extend(os.read(fd, 8192))
            except OSError: pass
        done, status = os.waitpid(pid, os.WNOHANG)
        if done:
            if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
                raise AssertionError(f'process failed: {status}, {output[-2500:]!r}')
            return bytes(output)
    os.kill(pid, signal.SIGKILL)
    os.waitpid(pid, 0)
    raise AssertionError(f'process did not exit: {output[-2500:]!r}')

pid, fd = start(); output = bytearray()
read_until(pid, fd, 'Create AACT shared vault', output)
os.write(fd, b'first launch actual password\r')
read_until(pid, fd, 'Confirm ma', output)
os.write(fd, b'first launch actual password\r')
read_until(pid, fd, 'Capability', output)
os.write(fd, b'\x03')
raw = finish(pid, fd, output)
assert b'first launch actual password' not in raw, 'new master password appeared in TUI output'

pid, fd = start(); output = bytearray()
read_until(pid, fd, 'Unlock AACT shared vault', output)
os.write(fd, b'wrong launch password\r')
read_until(pid, fd, 'Could not unlock the vault', output)
os.write(fd, b'first launch actual password\r')
read_until(pid, fd, 'Capability', output)
os.write(fd, b'\x03')
raw = finish(pid, fd, output)
assert b'wrong launch password' not in raw and b'first launch actual password' not in raw, 'password appeared in TUI output'

pid, fd = start(); output = bytearray()
read_until(pid, fd, 'Unlock AACT shared vault', output)
os.write(fd, b'\x1b')
raw = finish(pid, fd, output)
assert b'first launch actual password' not in raw, 'password appeared after cancel'
print('PTY passed: first-run creation+migration, fresh unlock with wrong-password retry, cancel, and no echoed passwords.')
