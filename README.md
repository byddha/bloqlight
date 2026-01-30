# bloqlight

Driver for the ROBOBLOQ USB Light Bar. Reverse engineered the HID protocol to control LEDs directly — official software was not available on Linux. Works as a UDP-to-HID bridge for HyperHDR. Single binary acts as both server (receives from HyperHDR) and CLI client (control via TCP). Supports brightness control, manual color override, and seamless switching between HyperHDR and manual modes.

Should work on Windows too (untested) if you want HyperHDR support there.

Learned Go while writing this

## Usage

```bash
go build ./cmd/bloqlight

# Start server (listens for HyperHDR on UDP :5568, CLI control on TCP :5569)
./bloqlight

# CLI commands (while server is running)
./bloqlight set 255,128,0      # set color (RGB)
./bloqlight set off            # turn off LEDs
./bloqlight mode manual        # ignore HyperHDR, keep current color
./bloqlight mode hyper         # resume listening to HyperHDR
./bloqlight brightness 50      # set brightness 0-100 (affects all output)
./bloqlight brightness         # get current brightness
./bloqlight status             # show mode, ports, LED count, etc.
```

Configure HyperHDR: LED Hardware → Type: `udpraw` → Port: `5568`

Server flags: `-port` (UDP port), `-debug` (verbose logging), `-list` (show HID devices)

## udev rules (Linux)

Create `/etc/udev/rules.d/99-bloqlight.rules`:

```
SUBSYSTEM=="hidraw", ATTRS{idVendor}=="1a86", ATTRS{idProduct}=="fe07", MODE="0666"
SUBSYSTEM=="usb", ATTR{idVendor}=="1a86", ATTR{idProduct}=="fe07", MODE="0666"
ACTION=="add", SUBSYSTEM=="usb", ATTRS{idVendor}=="1a86", ATTRS{idProduct}=="fe07", ATTR{bInterfaceNumber}=="01", ATTR{authorized}="0"
```

Then reload:

```bash
sudo udevadm control --reload-rules
# unplug and replug device
```

The last rule blocks the keyboard interface — the device tries to inject keystrokes (SUPER + R -> types link to official download page and enter) on connect.

## systemd setup

```bash
sudo cp bloqlight /usr/local/bin/
sudo cp contrib/systemd/bloqlight.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now bloqlight
```

## Acknowledgments

[singto1597/syncLight-Robobloq-Linux](https://github.com/singto1597/syncLight-Robobloq-Linux) -initial reference.

