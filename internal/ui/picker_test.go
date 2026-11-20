package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPickWithEnvCrossPlatform(t *testing.T) {
	t.Run("windows folder and bgFile pass YTMIX_PICK_INIT and parse PICKED/CANCELLED", func(t *testing.T) {
		var gotName string
		var gotArgs []string
		var gotEnv []string

		env := pickerEnv{
			goos: "windows",
			lookPath: func(bin string) (string, error) {
				if bin == "powershell.exe" {
					return `C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, nil
				}
				return "", errors.New("not found")
			},
			getenv: func(string) string { return "" },
			runCmd: func(_ context.Context, name string, args []string, extraEnv []string) (string, string, int, error) {
				gotName = name
				gotArgs = args
				gotEnv = extraEnv
				return "PICKED:D:\\Music\\Ambient\r\n", "", 0, nil
			},
		}

		picked, err := pickWithEnv(context.Background(), pickKindFolder, `D:\Music`, env)
		if err != nil {
			t.Fatalf("pickWithEnv folder error: %v", err)
		}
		if picked != `D:\Music\Ambient` {
			t.Fatalf("picked = %q, want D:\\Music\\Ambient", picked)
		}
		if !strings.HasSuffix(strings.ToLower(gotName), "powershell.exe") {
			t.Fatalf("expected powershell.exe, got %q", gotName)
		}
		if len(gotEnv) != 1 || gotEnv[0] != `YTMIX_PICK_INIT=D:\Music` {
			t.Fatalf("extraEnv = %v, want [YTMIX_PICK_INIT=D:\\Music]", gotEnv)
		}
		if len(gotArgs) < 5 || gotArgs[2] != "-STA" {
			t.Fatalf("expected -STA flag in powershell args, got %v", gotArgs)
		}

		// Cancellation returns ErrPickerCancelled.
		env.runCmd = func(_ context.Context, _ string, _ []string, _ []string) (string, string, int, error) {
			return "CANCELLED\r\n", "", 0, nil
		}
		if _, err := pickWithEnv(context.Background(), pickKindBGFile, "", env); !errors.Is(err, ErrPickerCancelled) {
			t.Fatalf("expected ErrPickerCancelled, got %v", err)
		}
	})

	t.Run("darwin uses osascript and treats -128 as cancellation", func(t *testing.T) {
		env := pickerEnv{
			goos: "darwin",
			lookPath: func(bin string) (string, error) {
				if bin == "osascript" {
					return "/usr/bin/osascript", nil
				}
				return "", errors.New("not found")
			},
			getenv: func(string) string { return "" },
			runCmd: func(_ context.Context, name string, args []string, extraEnv []string) (string, string, int, error) {
				if name != "/usr/bin/osascript" || len(args) != 2 || args[0] != "-e" {
					t.Fatalf("unexpected osascript invocation: %s %v", name, args)
				}
				if len(extraEnv) != 1 || extraEnv[0] != "YTMIX_PICK_INIT=/Users/test/Music" {
					t.Fatalf("unexpected env: %v", extraEnv)
				}
				return "/Users/test/Music/Mix\n", "", 0, nil
			},
		}

		picked, err := pickWithEnv(context.Background(), pickKindFolder, "/Users/test/Music", env)
		if err != nil || picked != "/Users/test/Music/Mix" {
			t.Fatalf("darwin pick = %q, err=%v", picked, err)
		}

		env.runCmd = func(_ context.Context, _ string, _ []string, _ []string) (string, string, int, error) {
			return "", "execution error: User canceled. (-128)", 1, errors.New("exit 1")
		}
		if _, err := pickWithEnv(context.Background(), pickKindBGFile, "", env); !errors.Is(err, ErrPickerCancelled) {
			t.Fatalf("expected ErrPickerCancelled on -128, got %v", err)
		}
	})

	t.Run("linux requires GUI display and prefers zenity then kdialog", func(t *testing.T) {
		headless := pickerEnv{
			goos:     "linux",
			lookPath: func(string) (string, error) { return "/usr/bin/zenity", nil },
			getenv:   func(string) string { return "" },
		}
		if _, err := pickWithEnv(context.Background(), pickKindFolder, "", headless); !errors.Is(err, ErrPickerUnavailable) {
			t.Fatalf("expected ErrPickerUnavailable when headless, got %v", err)
		}

		zenityEnv := pickerEnv{
			goos: "linux",
			lookPath: func(bin string) (string, error) {
				if bin == "zenity" {
					return "/usr/bin/zenity", nil
				}
				return "", errors.New("not found")
			},
			getenv: func(k string) string {
				if k == "DISPLAY" {
					return ":0"
				}
				return ""
			},
			runCmd: func(_ context.Context, name string, args []string, _ []string) (string, string, int, error) {
				if name != "/usr/bin/zenity" {
					t.Fatalf("expected zenity, got %q", name)
				}
				if len(args) < 3 || args[0] != "--file-selection" || args[1] != "--directory" {
					t.Fatalf("unexpected zenity args: %v", args)
				}
				return "/mnt/media/lofi\n", "", 0, nil
			},
		}
		picked, err := pickWithEnv(context.Background(), pickKindFolder, "/mnt/media", zenityEnv)
		if err != nil || picked != "/mnt/media/lofi" {
			t.Fatalf("zenity pick = %q, err=%v", picked, err)
		}

		zenityEnv.runCmd = func(_ context.Context, _ string, _ []string, _ []string) (string, string, int, error) {
			return "", "", 1, errors.New("exit 1")
		}
		if _, err := pickWithEnv(context.Background(), pickKindFolder, "", zenityEnv); !errors.Is(err, ErrPickerCancelled) {
			t.Fatalf("expected ErrPickerCancelled for zenity exit 1, got %v", err)
		}

		// kdialog fallback when zenity is missing.
		kdialogEnv := pickerEnv{
			goos: "linux",
			lookPath: func(bin string) (string, error) {
				if bin == "kdialog" {
					return "/usr/bin/kdialog", nil
				}
				return "", errors.New("not found")
			},
			getenv: func(k string) string {
				if k == "WAYLAND_DISPLAY" {
					return "wayland-0"
				}
				return ""
			},
			runCmd: func(_ context.Context, name string, args []string, _ []string) (string, string, int, error) {
				if name != "/usr/bin/kdialog" || len(args) == 0 || args[0] != "--getopenfilename" {
					t.Fatalf("unexpected kdialog call: %s %v", name, args)
				}
				return "/home/user/cover.png\n", "", 0, nil
			},
		}
		bg, err := pickWithEnv(context.Background(), pickKindBGFile, "/home/user", kdialogEnv)
		if err != nil || bg != "/home/user/cover.png" {
			t.Fatalf("kdialog bgFile = %q, err=%v", bg, err)
		}
	})

	t.Run("rejects unknown picker kind", func(t *testing.T) {
		if _, err := pickWithEnv(context.Background(), "invalid", "", pickerEnv{goos: "linux"}); err == nil {
			t.Fatalf("expected error for invalid picker kind")
		}
	})
}
