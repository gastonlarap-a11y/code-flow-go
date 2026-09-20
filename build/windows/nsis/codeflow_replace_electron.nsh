; CodeFlow 3.0: remove a CodeFlow 2.x install (electron-builder, per-user) before installing over it.
;
; This file is CodeFlow's, not Wails'. It is included from project.nsi — never from wails_tools.nsh,
; which the CLI regenerates and would overwrite.
;
; Two things happen before the install, in this order, and both were measured on a Windows runner
; with 2.7.1 installed and running (MIGRATION-GO.md §10.3, probe W-3):
;
;   1. cf.closeRunningCodeFlow  — close anything of ours that is running
;   2. cf.uninstallElectronCodeFlow — run 2.x's own uninstaller if it is registered
;
; The GUID is UUIDv5("com.codeflow.app", 50e065bc-3134-11e6-9bab-38c9862bdaf3), which is how
; electron-builder 26.16.1 derives the per-user uninstall key. It is matched on the **GUID** and
; never on DisplayName: that reads `CodeFlow <version>` and would stop matching on the next release.

!include "LogicLib.nsh"

!define CF_ELECTRON_GUID "e452a328-6f16-5dfd-9ae4-7f7f7761c215"
!define CF_ELECTRON_UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${CF_ELECTRON_GUID}"
!define CF_ELECTRON_INSTALL_KEY "Software\${CF_ELECTRON_GUID}"

!macro CF_PROCESS_RUNNING NAME OUTVAR
  nsExec::ExecToStack 'cmd /C tasklist /NH /FI "IMAGENAME eq ${NAME}" | find /I "${NAME}"'
  Pop ${OUTVAR}
  Pop $9
!macroend

; cf.closeRunningCodeFlow closes a running CodeFlow — 2.x or 3.x — before the install.
;
; Belt and braces for the 2.x case: W-2 showed the old uninstaller closes a running 2.7.1 by itself
; in silent mode. It is **not** belt and braces for 3.x → 3.y: the updater starts this installer
; without quitting (§2.9, BOOT-021), so a 3.0 → 3.1 update always arrives with CodeFlow.exe running,
; and Wails' own template has no running-app check at all.
Function cf.closeRunningCodeFlow
  !insertmacro CF_PROCESS_RUNNING "CodeFlow.exe" $0
  !insertmacro CF_PROCESS_RUNNING "codeflow-core.exe" $1
  ${If} $0 == 0
  ${OrIf} $1 == 0
    MessageBox MB_OKCANCEL|MB_ICONEXCLAMATION "CodeFlow is running and will be closed to continue." /SD IDOK IDOK cf_kill
    Abort
    cf_kill:
    nsExec::ExecToStack 'taskkill /F /T /IM CodeFlow.exe'
    Pop $2
    Pop $9
    nsExec::ExecToStack 'taskkill /F /T /IM codeflow-core.exe'
    Pop $2
    Pop $9
    Sleep 1500
  ${EndIf}
FunctionEnd

; cf.uninstallElectronCodeFlow runs 2.x's own uninstaller, if one is registered.
;
; The uninstaller is copied out of the directory it is about to delete and run with `_?=`, which is
; what makes ExecWait actually wait — electron-builder's own `uninstallOldVersion` does the same.
; `/KEEP_APP_DATA --updated` keep electron-builder's app-data handling out of the way; the user's
; data lives in C:\CodeFlow either way (BOOT-003, DIVERGENCE-BOOT-a), and 3.0 must find it there.
;
; A failure is fatal rather than ignored: continuing would leave two CodeFlows registered, with the
; 2.x shortcuts still pointing at a build that is about to be half-overwritten.
Function cf.uninstallElectronCodeFlow
  ReadRegStr $R0 HKCU "${CF_ELECTRON_UNINSTALL_KEY}" "UninstallString"
  ${If} $R0 == ""
    Return
  ${EndIf}
  ReadRegStr $R1 HKCU "${CF_ELECTRON_INSTALL_KEY}" "InstallLocation"
  ${If} $R1 == ""
    StrCpy $R1 "$LOCALAPPDATA\Programs\CodeFlow"
  ${EndIf}
  ${IfNot} ${FileExists} "$R1\Uninstall CodeFlow.exe"
    Return
  ${EndIf}
  InitPluginsDir
  CopyFiles /SILENT "$R1\Uninstall CodeFlow.exe" "$PLUGINSDIR\old-uninstaller.exe"
  StrCpy $R4 0
  cf_retry:
    IntOp $R4 $R4 + 1
    ExecWait '"$PLUGINSDIR\old-uninstaller.exe" /S /KEEP_APP_DATA /currentuser --updated _?=$R1' $R3
    ${If} $R3 != 0
    ${AndIf} $R4 < 5
      Sleep 1000
      Goto cf_retry
    ${EndIf}
  ${If} $R3 != 0
    MessageBox MB_OK|MB_ICONSTOP "CodeFlow 2.x could not be removed (exit $R3). Close it and run this installer again." /SD IDOK
    Abort
  ${EndIf}
  Delete "$R1\Uninstall CodeFlow.exe"
FunctionEnd
