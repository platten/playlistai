# A skipped executable is a failed update, even when NSIS runs with /S.
# Keep this separate from the generated Wails include.
!macro playlistai.installFiles
    SetOverwrite try
    playlistai_copy_retry:
    ClearErrors
    SetOutPath $INSTDIR
    !insertmacro wails.files
    IfErrors playlistai_copy_failed playlistai_copy_done
    playlistai_copy_failed:
    MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "Playlist AI could not be replaced. Close all Playlist AI windows and wait for background analysis to finish, then choose Retry. If the problem persists, restart Windows and run this installer again." /SD IDCANCEL IDRETRY playlistai_copy_retry
    SetErrorLevel 1
    Abort "Playlist AI was not updated."
    playlistai_copy_done:
    SetOverwrite on
!macroend
