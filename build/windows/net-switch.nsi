Unicode true
!include "MUI2.nsh"

!ifndef VERSION
  !error "VERSION is required"
!endif
!ifndef ARCHITECTURE
  !error "ARCHITECTURE is required"
!endif
!ifndef BINARY_PATH
  !error "BINARY_PATH is required"
!endif
!ifndef ICON_PATH
  !error "ICON_PATH is required"
!endif
!ifndef OUTPUT_PATH
  !error "OUTPUT_PATH is required"
!endif

Name "Net Switch"
OutFile "${OUTPUT_PATH}"
InstallDir "$LOCALAPPDATA\Programs\Net Switch"
InstallDirRegKey HKCU "Software\Net Switch" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma
Icon "${ICON_PATH}"
UninstallIcon "${ICON_PATH}"

VIProductVersion "${VERSION}.0"
VIAddVersionKey /LANG=1033 "ProductName" "Net Switch"
VIAddVersionKey /LANG=1033 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "FileDescription" "Net Switch installer (${ARCHITECTURE})"
VIAddVersionKey /LANG=1033 "LegalCopyright" "Net Switch Contributors"

!define MUI_ABORTWARNING
!define MUI_ICON "${ICON_PATH}"
!define MUI_UNICON "${ICON_PATH}"
!define MUI_FINISHPAGE_RUN "$INSTDIR\net-switch.exe"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

Section "Install" SEC_INSTALL
  SetShellVarContext current
  SetOutPath "$INSTDIR"
  File /oname=net-switch.exe "${BINARY_PATH}"
  File /oname=net-switch.ico "${ICON_PATH}"
  WriteUninstaller "$INSTDIR\Uninstall.exe"

  CreateDirectory "$SMPROGRAMS\Net Switch"
  CreateShortcut "$SMPROGRAMS\Net Switch\Net Switch.lnk" "$INSTDIR\net-switch.exe" "" "$INSTDIR\net-switch.ico"
  CreateShortcut "$SMPROGRAMS\Net Switch\卸载 Net Switch.lnk" "$INSTDIR\Uninstall.exe" "" "$INSTDIR\net-switch.ico"

  WriteRegStr HKCU "Software\Net Switch" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Net Switch" "DisplayName" "Net Switch"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Net Switch" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Net Switch" "Publisher" "Net Switch Contributors"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Net Switch" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Net Switch" "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Net Switch" "NoRepair" 1
SectionEnd

Section "Uninstall"
  SetShellVarContext current
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "NetSwitch"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\Net Switch"
  DeleteRegKey HKCU "Software\Net Switch"

  Delete "$SMPROGRAMS\Net Switch\Net Switch.lnk"
  Delete "$SMPROGRAMS\Net Switch\卸载 Net Switch.lnk"
  RMDir "$SMPROGRAMS\Net Switch"

  Delete "$INSTDIR\net-switch.exe"
  Delete "$INSTDIR\net-switch.ico"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
SectionEnd
