package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	pickKindFolder = "folder"
	pickKindBGFile = "bgFile"
)

var (
	// ErrPickerCancelled indicates the user dismissed or cancelled the native OS dialog.
	ErrPickerCancelled = errors.New("picker cancelled by user")
	// ErrPickerUnavailable indicates native OS dialog tooling is absent or headless.
	ErrPickerUnavailable = errors.New("native OS picker is unavailable in this environment")
)

// PickPathFunc opens a native OS file or folder selection dialog and returns the selected path.
type PickPathFunc func(ctx context.Context, kind, initialDir string) (string, error)

type pickerEnv struct {
	goos     string
	lookPath func(string) (string, error)
	getenv   func(string) string
	runCmd   func(ctx context.Context, name string, args []string, extraEnv []string) (stdout, stderr string, exitCode int, err error)
}

func newDefaultPickPath(goos string, lookPath func(string) (string, error)) PickPathFunc {
	if goos == "" {
		goos = runtime.GOOS
	}
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	env := pickerEnv{
		goos:     goos,
		lookPath: lookPath,
		getenv:   os.Getenv,
		runCmd:   defaultPickerRunCmd,
	}
	return func(ctx context.Context, kind, initialDir string) (string, error) {
		return pickWithEnv(ctx, kind, initialDir, env)
	}
}

func defaultPickerRunCmd(ctx context.Context, name string, args []string, extraEnv []string) (string, string, int, error) {
	// #nosec G204 -- picker executes OS-native dialog binaries (powershell.exe/osascript/zenity/kdialog) resolved via LookPath with fixed script templates and env-passed initial directory.
	cmd := exec.CommandContext(ctx, name, args...)
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return stdoutBuf.String(), stderrBuf.String(), exitCode, err
}

func pickWithEnv(ctx context.Context, kind, initialDir string, env pickerEnv) (string, error) {
	if kind != pickKindFolder && kind != pickKindBGFile {
		return "", fmt.Errorf("unsupported picker kind %q", kind)
	}
	var extraEnv []string
	if trimmed := strings.TrimSpace(initialDir); trimmed != "" {
		extraEnv = []string{"YTMIX_PICK_INIT=" + trimmed}
	}

	switch env.goos {
	case "windows":
		return pickWindows(ctx, kind, extraEnv, env)
	case "darwin":
		return pickDarwin(ctx, kind, extraEnv, env)
	default:
		return pickLinux(ctx, kind, strings.TrimSpace(initialDir), env)
	}
}

func pickWindows(ctx context.Context, kind string, extraEnv []string, env pickerEnv) (string, error) {
	psPath, err := env.lookPath("powershell.exe")
	if err != nil {
		psPath, err = env.lookPath("powershell")
		if err != nil {
			return "", fmt.Errorf("%w: powershell not found", ErrPickerUnavailable)
		}
	}
	script := windowsFolderPickerScript
	if kind == pickKindBGFile {
		script = windowsBGFilePickerScript
	}
	stdout, stderr, _, runErr := env.runCmd(ctx, psPath, []string{"-NoProfile", "-NonInteractive", "-STA", "-Command", script}, extraEnv)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	picked, parseErr := parseMarkedPickerOutput(stdout)
	if parseErr == nil {
		return picked, nil
	}
	if errors.Is(parseErr, ErrPickerCancelled) {
		return "", ErrPickerCancelled
	}
	detail := strings.TrimSpace(stderr)
	if detail == "" && runErr != nil {
		detail = runErr.Error()
	}
	if detail == "" {
		return "", ErrPickerUnavailable
	}
	return "", fmt.Errorf("%w: %s", ErrPickerUnavailable, detail)
}

func pickDarwin(ctx context.Context, kind string, extraEnv []string, env pickerEnv) (string, error) {
	osaPath, err := env.lookPath("osascript")
	if err != nil {
		return "", fmt.Errorf("%w: osascript not found", ErrPickerUnavailable)
	}
	script := macFolderPickerScript
	if kind == pickKindBGFile {
		script = macBGFilePickerScript
	}
	stdout, stderr, _, runErr := env.runCmd(ctx, osaPath, []string{"-e", script}, extraEnv)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if runErr != nil {
		lowerErr := strings.ToLower(stderr)
		if strings.Contains(stderr, "-128") || strings.Contains(lowerErr, "user canceled") || strings.Contains(lowerErr, "user cancelled") {
			return "", ErrPickerCancelled
		}
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = runErr.Error()
		}
		return "", fmt.Errorf("%w: %s", ErrPickerUnavailable, detail)
	}
	picked := strings.TrimSpace(stdout)
	if picked == "" {
		return "", ErrPickerCancelled
	}
	return picked, nil
}

func pickLinux(ctx context.Context, kind, initialDir string, env pickerEnv) (string, error) {
	if strings.TrimSpace(env.getenv("DISPLAY")) == "" && strings.TrimSpace(env.getenv("WAYLAND_DISPLAY")) == "" {
		return "", fmt.Errorf("%w: no DISPLAY or WAYLAND_DISPLAY session", ErrPickerUnavailable)
	}

	if zenityPath, err := env.lookPath("zenity"); err == nil {
		var args []string
		if kind == pickKindFolder {
			args = []string{"--file-selection", "--directory", "--title=Choose Folder"}
		} else {
			args = []string{
				"--file-selection",
				"--title=Choose Background Visual",
				"--file-filter=Background Visuals | *.jpg *.jpeg *.png *.webp *.mp4 *.mov *.gif *.JPG *.JPEG *.PNG *.WEBP *.MP4 *.MOV *.GIF",
			}
		}
		if initialDir != "" {
			args = append(args, "--filename="+strings.TrimRight(filepath.ToSlash(initialDir), "/")+"/")
		}
		stdout, stderr, exitCode, runErr := env.runCmd(ctx, zenityPath, args, nil)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if runErr == nil {
			picked := strings.TrimSpace(stdout)
			if picked == "" {
				return "", ErrPickerCancelled
			}
			return picked, nil
		}
		if exitCode == 1 || exitCode == 5 {
			return "", ErrPickerCancelled
		}
		if detail := strings.TrimSpace(stderr); detail != "" {
			return "", fmt.Errorf("%w: %s", ErrPickerUnavailable, detail)
		}
	}

	if kdialogPath, err := env.lookPath("kdialog"); err == nil {
		startDir := initialDir
		if startDir == "" {
			startDir = "."
		}
		var args []string
		if kind == pickKindFolder {
			args = []string{"--getexistingdirectory", startDir}
		} else {
			args = []string{"--getopenfilename", startDir, "*.jpg *.jpeg *.png *.webp *.mp4 *.mov *.gif|Background Visuals"}
		}
		stdout, stderr, exitCode, runErr := env.runCmd(ctx, kdialogPath, args, nil)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if runErr == nil {
			picked := strings.TrimSpace(stdout)
			if picked == "" {
				return "", ErrPickerCancelled
			}
			return picked, nil
		}
		if exitCode == 1 {
			return "", ErrPickerCancelled
		}
		detail := strings.TrimSpace(stderr)
		if detail == "" {
			detail = runErr.Error()
		}
		return "", fmt.Errorf("%w: %s", ErrPickerUnavailable, detail)
	}

	return "", fmt.Errorf("%w: neither zenity nor kdialog found", ErrPickerUnavailable)
}

func parseMarkedPickerOutput(stdout string) (string, error) {
	for _, rawLine := range strings.Split(stdout, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		if line == "CANCELLED" {
			return "", ErrPickerCancelled
		}
		if strings.HasPrefix(line, "PICKED:") {
			val := strings.TrimSpace(strings.TrimPrefix(line, "PICKED:"))
			if val == "" {
				return "", ErrPickerCancelled
			}
			return val, nil
		}
	}
	return "", ErrPickerUnavailable
}

const windowsFolderPickerScript = `
$code = @"
using System;
using System.Runtime.InteropServices;

public static class YtmixFolderPicker {
    [ComImport, Guid("DC1C5A9C-E88A-4dde-A5A1-60F82A20AEF7")]
    private class FileOpenDialogRCW {}

    [ComImport, Guid("42f85136-db7e-439c-85f1-e4075d135fc8"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    private interface IFileOpenDialog {
        [PreserveSig] int Show(IntPtr parent);
        void SetFileTypes(uint cFileTypes, IntPtr rgFilterSpec);
        void SetFileTypeIndex(uint iFileType);
        void GetFileTypeIndex(out uint piFileType);
        void Advise(IntPtr pfde, out uint pdwCookie);
        void Unadvise(uint dwCookie);
        void SetOptions(uint fos);
        void GetOptions(out uint pfos);
        void SetDefaultFolder(IShellItem psi);
        void SetFolder(IShellItem psi);
        void GetFolder(out IShellItem ppsi);
        void GetCurrentSelection(out IShellItem ppsi);
        void SetFileName([MarshalAs(UnmanagedType.LPWStr)] string pszName);
        void GetFileName([MarshalAs(UnmanagedType.LPWStr)] out string pszName);
        void SetTitle([MarshalAs(UnmanagedType.LPWStr)] string pszTitle);
        void SetOkButtonLabel([MarshalAs(UnmanagedType.LPWStr)] string pszText);
        void SetFileNameLabel([MarshalAs(UnmanagedType.LPWStr)] string pszLabel);
        void GetResult(out IShellItem ppsi);
    }

    [ComImport, Guid("43826d1e-e718-42ee-bc55-a1e261c37bfe"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    private interface IShellItem {
        void BindToHandler(IntPtr pbc, [MarshalAs(UnmanagedType.LPStruct)] Guid bhid, [MarshalAs(UnmanagedType.LPStruct)] Guid riid, out IntPtr ppv);
        void GetParent(out IShellItem ppsi);
        void GetDisplayName(uint sigdnName, [MarshalAs(UnmanagedType.LPWStr)] out string ppszName);
        void GetAttributes(uint sfgaoMask, out uint psfgaoAttribs);
        void Compare(IShellItem psi, uint hint, out int piOrder);
    }

    [DllImport("shell32.dll", CharSet = CharSet.Unicode, PreserveSig = false)]
    private static extern void SHCreateItemFromParsingName(string pszPath, IntPtr pbc, [MarshalAs(UnmanagedType.LPStruct)] Guid riid, out IShellItem ppv);

    public static string PickFolder(string initialDir, string title) {
        var dialog = (IFileOpenDialog)new FileOpenDialogRCW();
        uint opts;
        dialog.GetOptions(out opts);
        dialog.SetOptions(opts | 0x20u | 0x40u | 0x800u);
        if (!string.IsNullOrEmpty(title)) {
            dialog.SetTitle(title);
        }
        if (!string.IsNullOrEmpty(initialDir)) {
            try {
                IShellItem item;
                SHCreateItemFromParsingName(initialDir, IntPtr.Zero, typeof(IShellItem).GUID, out item);
                dialog.SetFolder(item);
            } catch {}
        }
        int hr = dialog.Show(IntPtr.Zero);
        if (hr == unchecked((int)0x800704C7)) return "";
        if (hr != 0) Marshal.ThrowExceptionForHR(hr);
        IShellItem result;
        dialog.GetResult(out result);
        string path;
        result.GetDisplayName(0x80058000u, out path);
        return path ?? "";
    }
}
"@
Add-Type -TypeDefinition $code
$initDir = $env:YTMIX_PICK_INIT
if ($initDir -and -not (Test-Path -LiteralPath $initDir -PathType Container)) { $initDir = "" }
$picked = [YtmixFolderPicker]::PickFolder($initDir, "Choose Folder")
if ([string]::IsNullOrWhiteSpace($picked)) {
    Write-Output "CANCELLED"
} else {
    Write-Output ("PICKED:" + $picked)
}
`

const windowsBGFilePickerScript = `
Add-Type -AssemblyName System.Windows.Forms
$owner = New-Object System.Windows.Forms.Form
$owner.TopMost = $true
$owner.StartPosition = [System.Windows.Forms.FormStartPosition]::Manual
$owner.Location = New-Object System.Drawing.Point(-32000, -32000)
$owner.Size = New-Object System.Drawing.Size(1, 1)
$owner.ShowInTaskbar = $false
try {
    $dlg = New-Object System.Windows.Forms.OpenFileDialog
    $dlg.Title = "Choose Background Visual"
    $dlg.Filter = "Background Visuals (*.jpg;*.jpeg;*.png;*.webp;*.mp4;*.mov;*.gif)|*.jpg;*.jpeg;*.png;*.webp;*.mp4;*.mov;*.gif|Images (*.jpg;*.jpeg;*.png;*.webp)|*.jpg;*.jpeg;*.png;*.webp|Videos & GIFs (*.mp4;*.mov;*.gif)|*.mp4;*.mov;*.gif|All files (*.*)|*.*"
    $dlg.CheckFileExists = $true
    $dlg.Multiselect = $false
    $initDir = $env:YTMIX_PICK_INIT
    if ($initDir -and (Test-Path -LiteralPath $initDir -PathType Container)) {
        $dlg.InitialDirectory = $initDir
    }
    $res = $dlg.ShowDialog($owner)
    if ($res -eq [System.Windows.Forms.DialogResult]::OK -and -not [string]::IsNullOrWhiteSpace($dlg.FileName)) {
        Write-Output ("PICKED:" + $dlg.FileName)
    } else {
        Write-Output "CANCELLED"
    }
} finally {
    $owner.Dispose()
}
`

const macFolderPickerScript = `
set initPath to ""
try
  set initPath to system attribute "YTMIX_PICK_INIT"
end try
if initPath is not "" then
  POSIX path of (choose folder with prompt "Choose Folder" default location (POSIX file initPath))
else
  POSIX path of (choose folder with prompt "Choose Folder")
end if
`

const macBGFilePickerScript = `
set initPath to ""
try
  set initPath to system attribute "YTMIX_PICK_INIT"
end try
set allowedTypes to {"public.jpeg", "public.png", "public.webp", "public.mpeg-4", "com.apple.quicktime-movie", "com.compuserve.gif"}
if initPath is not "" then
  POSIX path of (choose file with prompt "Choose Background Visual" of type allowedTypes default location (POSIX file initPath))
else
  POSIX path of (choose file with prompt "Choose Background Visual" of type allowedTypes)
end if
`
