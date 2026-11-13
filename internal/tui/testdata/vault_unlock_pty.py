import os, pty, select, signal, struct, sys, time

binary = os.path.abspath(sys.argv[1])
root = os.path.abspath(sys.argv[2])

def start(path, mode):
    pid, fd = pty.fork()
    if pid == 0:
        os.execv(binary, [binary, path, mode])
    fcntl = __import__('fcntl')
    termios = __import__('termios')
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack('HHHH', 24, 80, 0, 0))
    return pid, fd

def read_until(pid, fd, text, output, timeout=15):
    deadline = time.monotonic() + timeout
    while text.encode() not in output:
        if time.monotonic() > deadline:
            raise AssertionError(f'timed out waiting for {text!r}: {output[-2000:]!r}')
        ready, _, _ = select.select([fd], [], [], 0.1)
        if ready:
            try: chunk = os.read(fd, 4096)
            except OSError: break
            if not chunk: break
            output.extend(chunk)
        done, status = os.waitpid(pid, os.WNOHANG)
        if done: raise AssertionError(f'process exited before {text!r}: {status}, {output[-1000:]!r}')

def write(fd, text): os.write(fd, text.encode())
def finish(pid, fd, output, timeout=15):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        ready, _, _ = select.select([fd], [], [], 0.1)
        if ready:
            try: output.extend(os.read(fd, 4096))
            except OSError: pass
        done, status = os.waitpid(pid, os.WNOHANG)
        if done:
            if not os.WIFEXITED(status) or os.WEXITSTATUS(status) != 0:
                raise AssertionError(f'process failed: {status}, {output[-2000:]!r}')
            return bytes(output)
    os.kill(pid, signal.SIGKILL)
    os.waitpid(pid, 0)
    raise AssertionError(f'process did not exit: {output[-2000:]!r}')

existing = os.path.join(root, 'existing.kdbx')
seed = __import__('subprocess').run([binary, existing, 'seed'], timeout=15)
assert seed.returncode == 0
pid, fd = start(existing, 'unlock'); output = bytearray()
read_until(pid, fd, 'Master password', output)
write(fd, 'wrong actual password\r')
read_until(pid, fd, 'Could not unlock the vault', output)
write(fd, 'existing correct password\r')
raw = finish(pid, fd, output)
assert b'wrong actual password' not in raw and b'existing correct password' not in raw, 'password appeared in terminal output'

created = os.path.join(root, 'created.kdbx')
pid, fd = start(created, 'create'); output = bytearray()
read_until(pid, fd, 'Master password', output)
write(fd, 'created actual password\r')
time.sleep(1)
write(fd, 'created actual password\r')
raw = finish(pid, fd, output)
assert b'created actual password' not in raw, 'creation password appeared in terminal output'
check = __import__('subprocess').run([binary, created, 'check', 'created actual password'], timeout=15)
assert check.returncode == 0, 'created vault did not reopen with confirmed password'

cancel = os.path.join(root, 'cancel.kdbx')
pid, fd = start(cancel, 'create'); output = bytearray()
read_until(pid, fd, 'Master password', output)
write(fd, '\x1b')
raw = finish(pid, fd, output)
assert not os.path.exists(cancel), 'cancel unexpectedly created a vault'
print('PTY smoke passed: existing vault wrong-password retry, successful new-vault confirmation, cancel, and no echoed password.')
