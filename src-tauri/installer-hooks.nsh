; Tauri NSIS installer hooks.
;
; On upgrade, NSIS closes the main app but NOT its sidecars. If the app crashed,
; even the main exe can be left running. Any of these processes holding their
; .exe open makes NSIS fail with "Error opening file for writing ... ocr-sidecar.exe".
; Force-kill the app and its sidecars before extracting files so the overwrite
; always succeeds. taskkill on a missing process is a harmless no-op.

!macro NSIS_HOOK_PREINSTALL
  nsExec::Exec 'taskkill /F /T /IM "ocr-sidecar.exe"'
  nsExec::Exec 'taskkill /F /T /IM "engine.exe"'
  nsExec::Exec 'taskkill /F /T /IM "Auto ReUp Studio.exe"'
  Sleep 500
!macroend

!macro NSIS_HOOK_PREUNINSTALL
  nsExec::Exec 'taskkill /F /T /IM "ocr-sidecar.exe"'
  nsExec::Exec 'taskkill /F /T /IM "engine.exe"'
  nsExec::Exec 'taskkill /F /T /IM "Auto ReUp Studio.exe"'
  Sleep 500
!macroend