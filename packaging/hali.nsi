!ifndef VERSION
  !define VERSION "dev"
!endif
!ifndef REPOROOT
  !define REPOROOT "${__FILEDIR__}/.."
!endif

!define APP_NAME "Hali"
!define INSTALL_DIR "$PROGRAMFILES64\Hali"
!define REG_UNINSTALL "Software\Microsoft\Windows\CurrentVersion\Uninstall\Hali"
!define REG_ENV "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"
!define SVC_NAME "halid"

!ifndef HALI_BIN
  !define HALI_BIN "${REPOROOT}/dist/hali-windows-amd64.exe"
!endif

!ifndef HALID_BIN
  !define HALID_BIN "${REPOROOT}/dist/halid-windows-amd64.exe"
!endif

Name "${APP_NAME} ${VERSION}"
OutFile "${REPOROOT}/dist/hali-${VERSION}-windows-amd64-setup.exe"
InstallDir "${INSTALL_DIR}"
InstallDirRegKey HKLM "${REG_UNINSTALL}" "InstallLocation"
RequestExecutionLevel admin
SetCompressor lzma
Unicode true

!include "MUI2.nsh"
!include "WordFunc.nsh"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${REPOROOT}/LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Function StopAndDeleteService
  Exch $0

  ; Prevent service auto-restart loops while we replace binaries.
  ExecWait 'sc failure "$0" reset= 0 actions= ""'
  ExecWait 'sc stop "$0"'
  ExecWait 'taskkill /F /T /IM halid.exe'

  ; Wait briefly for SCM to fully transition to stopped/deleted state.
  Sleep 400
  ExecWait 'sc stop "$0"'
  Sleep 400

  ; Remove old registrations so upgrades can recreate service reliably.
  ExecWait 'sc delete "$0"'

  Pop $0
FunctionEnd

Function un.StopAndDeleteService
  Exch $0

  ; Prevent service auto-restart loops while we remove binaries.
  ExecWait 'sc failure "$0" reset= 0 actions= ""'
  ExecWait 'sc stop "$0"'
  ExecWait 'taskkill /F /T /IM halid.exe'

  Sleep 400
  ExecWait 'sc stop "$0"'
  Sleep 400

  ExecWait 'sc delete "$0"'

  Pop $0
FunctionEnd

Section "Install"
  ; Best effort cleanup for historical service names before replacing binaries.
  ; Keep this tolerant so fresh installs are not blocked when nothing is running.
  Push "${SVC_NAME}"
  Call StopAndDeleteService
  Push "HaliDaemon"
  Call StopAndDeleteService

  SetOutPath "${INSTALL_DIR}"

  File /oname=hali.exe "${HALI_BIN}"
  File /oname=halid.exe "${HALID_BIN}"

  ; Register hali:// URL protocol handler
  WriteRegStr HKCR "hali" "" "URL:Hali Protocol"
  WriteRegStr HKCR "hali" "URL Protocol" ""
  WriteRegStr HKCR "hali\DefaultIcon" "" '"${INSTALL_DIR}\hali.exe",0'
  WriteRegStr HKCR "hali\shell\open\command" "" '"${INSTALL_DIR}\hali.exe" open "%1"'

  ; Add install dir to system PATH
  ReadRegStr $0 HKLM "${REG_ENV}" "PATH"
  WriteRegStr HKLM "${REG_ENV}" "PATH" "$0;${INSTALL_DIR}"
  SendMessage ${HWND_BROADCAST} ${WM_WININICHANGE} 0 "STR:Environment" /TIMEOUT=5000

  ; Register Windows service
  ExecWait 'sc create "${SVC_NAME}" binPath= "${INSTALL_DIR}\halid.exe" start= auto DisplayName= "Hali Model Cache Daemon"'
  ExecWait 'sc description "${SVC_NAME}" "Hali daemon - caches and seeds LLM model files locally."'
  ExecWait 'sc start "${SVC_NAME}"'

  ; Uninstaller registration
  WriteRegStr HKLM "${REG_UNINSTALL}" "DisplayName" "Hali"
  WriteRegStr HKLM "${REG_UNINSTALL}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${REG_UNINSTALL}" "Publisher" "Hali Contributors"
  WriteRegStr HKLM "${REG_UNINSTALL}" "InstallLocation" "${INSTALL_DIR}"
  WriteRegStr HKLM "${REG_UNINSTALL}" "UninstallString" '"${INSTALL_DIR}\uninstall.exe"'
  WriteRegDWORD HKLM "${REG_UNINSTALL}" "NoModify" 1
  WriteRegDWORD HKLM "${REG_UNINSTALL}" "NoRepair" 1

  WriteUninstaller "${INSTALL_DIR}\uninstall.exe"
SectionEnd

Section "Uninstall"
  Push "${SVC_NAME}"
  Call un.StopAndDeleteService
  Push "HaliDaemon"
  Call un.StopAndDeleteService

  ; Remove hali:// URL protocol handler
  DeleteRegKey HKCR "hali"

  ; Remove install dir from system PATH
  ReadRegStr $0 HKLM "${REG_ENV}" "PATH"
  ${WordReplace} "$0" ";${INSTALL_DIR}" "" "+" $1
  WriteRegStr HKLM "${REG_ENV}" "PATH" "$1"
  SendMessage ${HWND_BROADCAST} ${WM_WININICHANGE} 0 "STR:Environment" /TIMEOUT=5000

  Delete "${INSTALL_DIR}\hali.exe"
  Delete "${INSTALL_DIR}\halid.exe"
  Delete "${INSTALL_DIR}\uninstall.exe"
  RMDir "${INSTALL_DIR}"

  DeleteRegKey HKLM "${REG_UNINSTALL}"
SectionEnd
