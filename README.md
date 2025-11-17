# **shed — Backup Manager**

### **Version 1.0.0**

<p align="center">
  <img src="assets/icon.png" alt="Shed icon" width="140">
</p>

---

## **Overview**

*Shed* is a cross-platform backup manager built in Go, featuring Restic integration, BitLocker unlock support, real-time file diffing, snapshot navigation, and a modern Fyne-based UI.

---

## **Requirements**

Install the following:

### 1. **Go (1.20 or later)**

Download from:
[https://go.dev/dl/](https://go.dev/dl/)

Verify installation:

```sh
go version
```

### 2. **Restic**

Download from:
[https://restic.net/](https://restic.net/)

Ensure `restic` is in your PATH.

### 3. **Windows-only (BitLocker jobs)**

* PowerShell 5+
* Admin privileges to unlock encrypted drives

---

## **Running the Application**

### **Normal mode**

From the project root:

```sh
go run .
```

### **Debug mode with logs**

```sh
go run . --logs
```

This enables verbose internal logging for troubleshooting backup jobs, BitLocker unlock attempts, snapshot parsing, diffs, etc.

---

## **Building / Compiling**

To build a standalone executable:

```sh
go build -o shed
```

On Windows:

```sh
go build -ldflags "-H=windowsgui" -o shed_1.0.0_windows.exe .
```

The resulting binary will appear in the current directory.

---

If you want, I can also generate:

✔ prebuilt binaries (Windows/Linux/macOS)
✔ a `Makefile` for automated builds
✔ packaging for `.zip` or installers
✔ a more detailed README with screenshots, features, and roadmap
