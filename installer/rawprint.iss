; Inno Setup 6 script. Build with:
;   iscc /DAppVersion=1.2.3 installer\rawprint.iss
; Expects dist\rawprint-agent-amd64.exe and dist\rawprint-agent-386.exe.
;
; Silent install for IT rollouts:
;   RawPrintAgent-Setup.exe /VERYSILENT /ORIGINS="https://pos.example.com" /PRINTERMATCH="LX-310"
;   Add /PORT=NNNNN only when the default port 18310 is taken on that PC.
; Optional: /AUTOAPPROVE=1 (skip pairing confirmation), /BROWSERPOLICY=0 (do not touch Chrome/Edge policy)

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif

[Setup]
AppId={{6F1F9E2C-3B5D-4C1E-9D7A-52A1C0B4E7D1}
AppName=RawPrint Agent
AppVersion={#AppVersion}
AppPublisher=RawPrint Agent contributors
AppPublisherURL=https://github.com/rizalahmaddd/raw-print-agent
DefaultDirName={autopf}\RawPrintAgent
DefaultGroupName=RawPrint Agent
DisableProgramGroupPage=yes
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
OutputDir=..\dist
OutputBaseFilename=RawPrintAgent-Setup-{#AppVersion}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
UninstallDisplayName=RawPrint Agent
LicenseFile=..\LICENSE

[Files]
Source: "..\dist\rawprint-agent-amd64.exe"; DestDir: "{app}"; DestName: "rawprint-agent.exe"; Check: Is64BitInstallMode; Flags: ignoreversion
Source: "..\dist\rawprint-agent-386.exe"; DestDir: "{app}"; DestName: "rawprint-agent.exe"; Check: not Is64BitInstallMode; Flags: ignoreversion
Source: "..\LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\README.md"; DestDir: "{app}"; Flags: ignoreversion

[INI]
Filename: "{group}\RawPrint Agent Settings.url"; Section: "InternetShortcut"; Key: "URL"; String: "{code:SettingsUrl}"

[UninstallDelete]
Type: files; Name: "{group}\RawPrint Agent Settings.url"

[Run]
Filename: "{app}\rawprint-agent.exe"; Parameters: "install {code:InstallArgs}"; StatusMsg: "Installing service..."; Flags: runhidden waituntilterminated
Filename: "{code:SettingsUrl}"; Description: "Open RawPrint Agent settings"; Flags: shellexec postinstall nowait skipifsilent

[UninstallRun]
Filename: "{app}\rawprint-agent.exe"; Parameters: "uninstall"; Flags: runhidden waituntilterminated; RunOnceId: "RemoveService"

[Code]
var
  OriginsPage: TInputQueryWizardPage;

procedure InitializeWizard;
begin
  OriginsPage := CreateInputQueryPage(wpSelectDir, 'Allowed web applications',
    'Which web applications may print through this agent?',
    'Enter the web origin(s), separated by commas, for example https://pos.example.com. ' +
    'Leave empty to configure later in the settings page.');
  OriginsPage.Add('Origins:', False);
  OriginsPage.Add('Default printer contains (optional, e.g. LX-310):', False);
  OriginsPage.Values[0] := ExpandConstant('{param:ORIGINS|}');
  OriginsPage.Values[1] := ExpandConstant('{param:PRINTERMATCH|}');
end;

function SettingsUrl(Param: String): String;
begin
  Result := 'http://127.0.0.1:' + ExpandConstant('{param:PORT|18310}') + '/';
end;

function InstallArgs(Param: String): String;
var
  Origins, Match: String;
begin
  Origins := Trim(OriginsPage.Values[0]);
  Match := Trim(OriginsPage.Values[1]);
  Result := '';
  if Origins <> '' then
    Result := Result + ' -origins "' + Origins + '"';
  if Match <> '' then
    Result := Result + ' -printer-match "' + Match + '"';
  if ExpandConstant('{param:PORT|}') <> '' then
    Result := Result + ' -port ' + ExpandConstant('{param:PORT|}');
  if ExpandConstant('{param:AUTOAPPROVE|0}') = '1' then
    Result := Result + ' -auto-approve';
  if (Origins <> '') and (ExpandConstant('{param:BROWSERPOLICY|1}') = '1') then
    Result := Result + ' -browser-policy';
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  Code: Integer;
begin
  // Stop the running service so the exe can be replaced during an upgrade.
  if FileExists(ExpandConstant('{app}\rawprint-agent.exe')) then
    Exec(ExpandConstant('{app}\rawprint-agent.exe'), 'stop', '', SW_HIDE, ewWaitUntilTerminated, Code);
  Result := '';
end;
