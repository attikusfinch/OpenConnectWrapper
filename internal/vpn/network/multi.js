// OpenConnect invokes Windows configuration scripts through cscript.exe.
// All values are inherited as data; no server-provided text becomes a command.
var shell = WScript.CreateObject("WScript.Shell");
var env = shell.Environment("Process");
var command = '"' + env("OCM_POWERSHELL") + '" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "' + env("OCM_NETWORK_ROOT") + '\\multi.ps1"';
WScript.Quit(shell.Run(command, 0, true));
