package main

import (
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// promptRepoPassword se asegura de que haya una contraseña disponible para el job.
// Si ya existe (RepoPassword o cache en memoria), llama directamente a onReady.
// Si no, muestra un diálogo pidiendo la contraseña, la cachea y luego llama a onReady.
func promptRepoPassword(job Job, title string, label string, win fyne.Window, onReady func(pw string)) {
	// Si ya tenemos contraseña en memoria o config
	if pw := effectivePassword(job); strings.TrimSpace(pw) != "" {
		onReady(pw)
		return
	}

	pwEntry := widget.NewPasswordEntry()

	pwForm := widget.NewForm(
		&widget.FormItem{Text: label, Widget: pwEntry},
	)

	// Declarar dlg con el TIPO CORRECTO
	var dlg *dialog.ConfirmDialog

	// Acción de OK (reutilizable por botón y Enter)
	okAction := func() {
		pw := strings.TrimSpace(pwEntry.Text)
		if pw == "" {
			dialog.ShowError(errors.New("password cannot be empty"), win)
			return
		}
		setJobPassword(job.Name, pw)
		dlg.Hide()
		onReady(pw)
	}

	// Crear el diálogo
	dlg = dialog.NewCustomConfirm(
		title,
		"OK",
		"Cancel",
		pwForm,
		func(ok bool) {
			if ok {
				okAction()
			}
		},
		win,
	)

	// ENTER = OK
	pwEntry.OnSubmitted = func(_ string) {
		okAction()
	}

	// Agrandar ventana
	dlg.Resize(fyne.NewSize(500, 220))

	dlg.Show()
}

// applyFileColumnWidths sets the widths of the file diff table columns using
// the values stored in the configuration. When no widths exist the previous
// fixed values are used. This function does not modify the configuration.
func applyFileColumnWidths() {
	if filesTable == nil {
		return
	}
	const fileCols = 5
	widths := cfg.FileColWidths
	if len(widths) != fileCols {
		// fallback a los valores fijos originales
		widths = []float32{30, 600, 80, 150, 150}
	}
	for col, w := range widths {
		filesTable.SetColumnWidth(col, w)
	}
}

// measureTextWidth returns an approximate pixel width for the given string
// using the current theme text size plus a small padding so that content does
// not touch the cell borders.
func measureTextWidth(s string) float32 {
	if s == "" {
		s = " "
	}
	sz := fyne.MeasureText(s, theme.TextSize(), fyne.TextStyle{})
	// padding a ambos lados
	return sz.Width + 16
}

// autoSnapshotColumnWidths inspects the snapshot data and computes column
// widths large enough to hold the visible content (ID, dates, host, etc.)
// without truncation where possible. The result can be stored in Config and
// reused on future runs.
func autoSnapshotColumnWidths() []float32 {
	const snapCols = 8
	widths := make([]float32, snapCols)
	headers := []string{"ID", "Date", "Time", "Host", "#Paths", "Δ", "Tags", "Last check"}
	for i, h := range headers {
		w := measureTextWidth(h)
		if w > widths[i] {
			widths[i] = w
		}
	}
	for _, s := range filterSnapshots {
		values := []string{
			s.ShortID,
			s.Time.Format("02/01/2006"),
			s.Time.Format("15:04:05"),
			s.Hostname,
			strconv.Itoa(len(s.Paths)),
			"", // Δ (no usamos datos reales acá)
			strings.Join(s.Tags, ","),
			"", // Last check (vacío la primera vez)
		}
		for i, v := range values {
			w := measureTextWidth(v)
			if w > widths[i] {
				widths[i] = w
			}
		}
	}
	// mínimos / máximos razonables
	min := []float32{80, 80, 70, 120, 60, 60, 120, 140}
	const maxWidth float32 = 700
	for i := range widths {
		if widths[i] < min[i] {
			widths[i] = min[i]
		}
		if widths[i] > maxWidth {
			widths[i] = maxWidth
		}
	}
	return widths
}

// autoFileColumnWidths inspects the file diff data and computes column widths
// based on the visible content. It is typically called after allFiles /
// filterFiles have been populated.
func autoFileColumnWidths() []float32 {
	const fileCols = 5
	widths := make([]float32, fileCols)
	headers := []string{"Δ", "Path", "Size", "Modified", "Created"}
	for i, h := range headers {
		w := measureTextWidth(h)
		if w > widths[i] {
			widths[i] = w
		}
	}
	for _, f := range filterFiles {
		values := []string{
			f.Change,
			f.Path,
			f.Size,
			f.MTime,
			f.CTime,
		}
		for i, v := range values {
			w := measureTextWidth(v)
			if w > widths[i] {
				widths[i] = w
			}
		}
	}
	// mínimos/máximos; Path puede ser más ancho
	min := []float32{30, 150, 70, 120, 120}
	max := []float32{80, 800, 200, 300, 300}
	for i := range widths {
		if widths[i] < min[i] {
			widths[i] = min[i]
		}
		if widths[i] > max[i] {
			widths[i] = max[i]
		}
	}
	return widths
}

// applySnapshotColumnWidths sets the widths of the snapshot table columns
// using the values stored in the configuration. When no widths have been
// persisted yet it falls back to sensible defaults that match the original
// fixed layout. This function does not modify the configuration.
func applySnapshotColumnWidths() {
	if snapTable == nil {
		return
	}

	const snapCols = 8

	// Si el config no tiene exactamente 8 columnas, usamos los defaults
	widths := cfg.SnapColWidths
	if len(widths) != snapCols {
		// ID, Date, Time, Host, #Paths, Δ, Tags, Last check
		widths = []float32{110, 90, 85, 160, 70, 80, 220, 170}
	}

	for col, w := range widths {
		snapTable.SetColumnWidth(col, w)
	}
}

// buildUI constructs the entire application user interface. It takes the
// application instance and returns the main window. The UI is divided into
// left and right panels. The left panel lists jobs and provides job
// management actions. The right panel lists snapshots and file differences.
// An about button is placed at the top right. Status messages are displayed
// in a bar at the bottom of the window.
func buildUI(myApp fyne.App) fyne.Window {
	win := myApp.NewWindow("Shed")
	if len(iconData) > 0 {
		win.SetIcon(fyne.NewStaticResource("icon.png", iconData))
	}
	win.Resize(fyne.NewSize(1200, 700))

	// about button anchored to top right
	aboutBtn := widget.NewButton("About", func() {
		ver := cfg.Version
		if strings.TrimSpace(ver) == "" {
			ver = appVersion
		}
		msg := fmt.Sprintf("Shed v%s\nDeveloper: tobiasrimoli@protonmail.com", ver)
		dialog.ShowInformation("About", msg, win)
	})

	statusLabel = widget.NewLabel("Ready")

	// ---- Jobs panel
	jobSearch = widget.NewEntry()
	jobSearch.SetPlaceHolder("Search jobs…")
	jobSearch.OnChanged = func(s string) {
		rebuildJobFilter(s)
		jobList.Refresh()
	}
	jobSearchBtn := widget.NewButtonWithIcon("", theme.SearchIcon(), func() {})
	jobSearchRow := container.NewBorder(nil, nil, nil, jobSearchBtn, jobSearch)
	rebuildJobFilter("")
	jobList = widget.NewList(
		func() int { return len(filterJobIdx) },
		func() fyne.CanvasObject { return widget.NewLabel("job") },
		func(i int, o fyne.CanvasObject) {
			idx := filterJobIdx[i]
			o.(*widget.Label).SetText(cfg.Jobs[idx].Name)
		},
	)
	// usar la variable global; inicializarla acá
	selectedJobIndex = -1

	addBtn := widget.NewButtonWithIcon("Add Job", theme.ContentAddIcon(), func() {
		showJobEditor(win, -1, jobList)
	})
	editBtn := widget.NewButton("Edit Job", func() {
		if selectedJobIndex < 0 || selectedJobIndex >= len(cfg.Jobs) {
			dialog.ShowInformation("Edit Job", "Please select a job first.", win)
			return
		}
		showJobEditor(win, selectedJobIndex, jobList)
	})
	deleteBtn := widget.NewButton("Delete Job", func() {
		if selectedJobIndex < 0 || selectedJobIndex >= len(cfg.Jobs) {
			dialog.ShowInformation("Delete Job", "Please select a job first.", win)
			return
		}
		job := cfg.Jobs[selectedJobIndex]
		dialog.ShowConfirm(
			"Delete Job",
			fmt.Sprintf("Delete job %q? Snapshots are NOT deleted from disk.", job.Name),
			func(ok bool) {
				if !ok {
					return
				}
				cfg.Jobs = append(cfg.Jobs[:selectedJobIndex], cfg.Jobs[selectedJobIndex+1:]...)
				if err := saveConfig(cfg); err != nil {
					dialog.ShowError(err, win)
					return
				}
				rebuildJobFilter(jobSearch.Text)
				jobList.Refresh()
				// clear snapshots and files
				allSnapshots = nil
				filterSnapshots = nil
				if snapTable != nil {
					snapTable.Refresh()
				}
				allFiles = nil
				filterFiles = nil
				if filesTable != nil {
					filesTable.Refresh()
				}
				selectedJobIndex = -1
				setStatus("No job selected")
				// update live backups
				startLiveBackups(cfg.ResticPath)
			}, win)
	})
	settingsBtn := widget.NewButtonWithIcon("Settings", theme.SettingsIcon(), func() {
		showSettings(win)
	})
	leftTopRow := container.NewHBox(addBtn, editBtn, deleteBtn, settingsBtn)
	leftTop := container.NewVBox(jobSearchRow, leftTopRow)
	left := container.NewBorder(nil, nil, nil, nil, container.NewBorder(leftTop, nil, nil, nil, jobList))

	// ---- Snapshots panel
	snapSearch := widget.NewEntry()
	snapSearch.SetPlaceHolder("Search snapshots…")
	snapSearchBtn := widget.NewButtonWithIcon("", theme.SearchIcon(), func() {})
	snapSearchRow := container.NewBorder(nil, nil, nil, snapSearchBtn, snapSearch)
	rebuildSnapFilter("")

	// Tabla de snapshots con columna Δ (resumen de cambios) y columna Last check
	snapTable = widget.NewTable(
		func() (int, int) { return len(filterSnapshots), 8 }, // 8 columnas: ID, Date, Time, Host, #Paths, Δ, Tags, Last check
		func() fyne.CanvasObject {
			// plantilla: tres textos horizontales, para Δ (coloreable) o texto simple
			t1 := canvas.NewText("", theme.ForegroundColor())
			t2 := canvas.NewText("", theme.ForegroundColor())
			t3 := canvas.NewText("", theme.ForegroundColor())
			ts := theme.TextSize()
			t1.TextSize = ts
			t2.TextSize = ts
			t3.TextSize = ts
			return container.NewHBox(t1, t2, t3)
		},
		func(id widget.TableCellID, o fyne.CanvasObject) {
			c := o.(*fyne.Container)
			t1 := c.Objects[0].(*canvas.Text)
			t2 := c.Objects[1].(*canvas.Text)
			t3 := c.Objects[2].(*canvas.Text)

			// reset básico
			fg := theme.ForegroundColor()
			t1.Color, t2.Color, t3.Color = fg, fg, fg
			t1.Text, t2.Text, t3.Text = "", "", ""

			row := id.Row
			if row < 0 || row >= len(filterSnapshots) {
				return
			}
			s := filterSnapshots[row]

			switch id.Col {
			case 0:
				idText := s.ShortID
				if selectedJobIndex >= 0 && selectedJobIndex < len(cfg.Jobs) {
					jobName := cfg.Jobs[selectedJobIndex].Name
					if rt, ok := snapshotRestoreTime(jobName, s.ShortID); ok && s.Time.Equal(rt) {
						idText = fmt.Sprintf("%s (restored)", s.ShortID)
					}
				}
				t1.Text = idText
			case 1:
				t1.Text = s.Time.Format("02/01/2006")
			case 2:
				t1.Text = s.Time.Format("15:04:05")
			case 3:
				t1.Text = s.Hostname
			case 4:
				t1.Text = strconv.Itoa(len(s.Paths))
			case 5:
				// columna Δ: resumen de cambios en colores
				if selectedJobIndex < 0 || selectedJobIndex >= len(cfg.Jobs) {
					return
				}
				job := cfg.Jobs[selectedJobIndex]

				// Si es una fila de "restored", buscamos el diff con la clave "<id> (restored)".
				keyID := s.ShortID
				if rt, ok := snapshotRestoreTime(job.Name, s.ShortID); ok && s.Time.Equal(rt) {
					keyID = restoredSnapshotKey(s.ShortID)
				}

				added, modified, deleted, ok := diffSummary(job, keyID)
				if !ok {
					// aún no hay diff cacheado para este snapshot
					return
				}

				// verde para +, amarillo para ≠, rojo para -
				if added > 0 {
					t1.Text = fmt.Sprintf("+%d", added)
					t1.Color = color.RGBA{0, 200, 0, 255}
				}
				if modified > 0 {
					t2.Text = fmt.Sprintf("≠%d", modified)
					t2.Color = color.RGBA{220, 180, 0, 255}
				}
				if deleted > 0 {
					t3.Text = fmt.Sprintf("-%d", deleted)
					t3.Color = color.RGBA{220, 0, 0, 255}
				}
			case 6:
				t1.Text = strings.Join(s.Tags, ",")
			case 7:
				// Last check: dd/mm/aaaa hh:mm:ss
				if selectedJobIndex < 0 || selectedJobIndex >= len(cfg.Jobs) {
					return
				}
				job := cfg.Jobs[selectedJobIndex]

				// Si estamos actualizando el Last check para este job,
				// mostramos un "spinner" textual en vez de la fecha.
				if isLastCheckUpdating(job.Name) {
					t1.Text = "⏳ Actualizando…"
					t2.Text = ""
					t3.Text = ""
					return
				}

				if ts, ok := getSnapshotLastCheck(job, s.ShortID); ok {
					t1.Text = ts.Format("02/01/2006 15:04:05")
					t2.Text = ""
					t3.Text = ""
				}
			}
		},
	)

	// usar fila de header nativa de Table para permitir resize por drag
	// usar fila de header nativa de Table para permitir resize por drag
	snapTable.ShowHeaderRow = true
	snapTable.CreateHeader = func() fyne.CanvasObject {
		btn := widget.NewButton("", nil)
		btn.Importance = widget.LowImportance
		btn.Alignment = widget.ButtonAlignLeading
		return btn
	}
	snapTable.UpdateHeader = func(id widget.TableCellID, o fyne.CanvasObject) {
		btn := o.(*widget.Button)

		if id.Row < 0 {
			col := id.Col
			text := ""
			sortable := false

			switch col {
			case 0:
				text = "ID"
				sortable = true
			case 1:
				text = "Date"
				sortable = true
			case 2:
				text = "Time"
				sortable = true
			case 3:
				text = "Host"
				sortable = true
			case 4:
				text = "#Paths"
				sortable = true
			case 5:
				text = "Δ" // no sortable (no tenemos datos precalculados)
			case 6:
				text = "Tags"
				sortable = true
			case 7:
				text = "Last check"
				sortable = true
			}

			btn.SetText(text)
			if sortable {
				btn.OnTapped = func() {
					handleSnapshotHeaderClick(col)
				}
			} else {
				btn.OnTapped = nil
			}
		} else {
			btn.SetText("")
			btn.OnTapped = nil
		}
	}

	applySnapshotColumnWidths()

	// ---- Files panel
	fileSearch := widget.NewEntry()
	fileSearch.SetPlaceHolder("Search files…")
	fileSearchBtn := widget.NewButtonWithIcon("", theme.SearchIcon(), func() {})
	fileSearchRow := container.NewBorder(nil, nil, nil, fileSearchBtn, fileSearch)
	rebuildFilesFilter("")
	filesTable = widget.NewTable(
		func() (int, int) { return len(filterFiles), 5 },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(id widget.TableCellID, o fyne.CanvasObject) {
			l := o.(*widget.Label)
			l.Wrapping = fyne.TextTruncate
			row := id.Row
			if row < 0 || row >= len(filterFiles) {
				l.SetText("")
				return
			}
			f := filterFiles[row]
			switch id.Col {
			case 0:
				l.SetText(f.Change)
			case 1:
				l.SetText(f.Path)
			case 2:
				l.SetText(f.Size)
			case 3:
				l.SetText(f.MTime)
			case 4:
				if f.CTime == "" {
					l.SetText("-")
				} else {
					l.SetText(f.CTime)
				}
			default:
				l.SetText("")
			}
		},
	)
	filesTable.ShowHeaderRow = true
	filesTable.CreateHeader = func() fyne.CanvasObject {
		btn := widget.NewButton("", nil)
		btn.Importance = widget.LowImportance
		btn.Alignment = widget.ButtonAlignLeading
		return btn
	}
	filesTable.UpdateHeader = func(id widget.TableCellID, o fyne.CanvasObject) {
		btn := o.(*widget.Button)

		if id.Row < 0 {
			col := id.Col
			text := ""

			switch col {
			case 0:
				text = "Δ"
			case 1:
				text = "Path"
			case 2:
				text = "Size"
			case 3:
				text = "Modified"
			case 4:
				text = "Created"
			}

			btn.SetText(text)
			if text != "" {
				btn.OnTapped = func() {
					handleFilesHeaderClick(col)
				}
			} else {
				btn.OnTapped = nil
			}
		} else {
			btn.SetText("")
			btn.OnTapped = nil
		}
	}
	applyFileColumnWidths()

	// ---- snapshot selection hook
	snapTable.OnSelected = func(id widget.TableCellID) {
		r := id.Row
		if r < 0 || r >= len(filterSnapshots) {
			return
		}
		selectedSnapIdx = r
		if selectedJobIndex < 0 || selectedJobIndex >= len(cfg.Jobs) {
			return
		}
		job := cfg.Jobs[selectedJobIndex]
		snap := filterSnapshots[selectedSnapIdx]
		setStatusSnapshot(&job, &snap)
		// load diff rows from cache or compute if missing
		go func(j Job, s Snapshot) {
			// Elegimos la clave de diff: normal o "<id> (restored)".
			keyID := s.ShortID
			if rt, ok := snapshotRestoreTime(j.Name, s.ShortID); ok && s.Time.Equal(rt) {
				keyID = restoredSnapshotKey(s.ShortID)
			}

			// Para restores NUNCA llamamos computeAndStoreLatestDiff (no queremos borrar snapshots ni tocar last-check).
			if _, ok := getDiffRows(j, keyID); !ok {
				// Sólo para snapshots "normales" calculamos el último diff automáticamente.
				if keyID == s.ShortID {
					if _, err := computeAndStoreLatestDiff(j, cfg.ResticPath, allSnapshots); err != nil {
						logPrintf("diff error: %v", err)
					}
				}
			}

			rows, ok := getDiffRows(j, keyID)
			if !ok {
				// nothing to show
				allFiles = nil
				rebuildFilesFilter(fileSearch.Text)
				filesTable.Refresh()
				return
			}
			allFiles = rows

			// Orden por defecto: Path ascendente.
			fileSortColumn = FileSortPath
			fileSortAsc = true
			sortFiles()

			rebuildFilesFilter(fileSearch.Text)

			// Auto-ajuste de columnas de files la primera vez que hay datos.
			if len(cfg.FileColWidths) == 0 {
				cfg.FileColWidths = autoFileColumnWidths()
				if err := saveConfig(cfg); err != nil && enableLogs {
					logPrintf("could not save file column widths: %v", err)
				}
			}
			applyFileColumnWidths()
			filesTable.Refresh()
		}(job, snap)
	}

	// ---- backup and restore buttons
	backupBtn := widget.NewButton("Backup now", func() {
		if selectedJobIndex < 0 || selectedJobIndex >= len(cfg.Jobs) {
			dialog.ShowInformation("Backup", "Please select a job first.", win)
			return
		}
		if cfg.ResticPath == "" {
			dialog.ShowInformation("Restic", "Please configure Restic in Settings.", win)
			return
		}
		job := cfg.Jobs[selectedJobIndex]

		// Caso BitLocker: no persistimos contraseña, la pedimos (o usamos cache)
		if job.Bitlocker {
			promptRepoPassword(job, "Enter Password", "BitLocker & Backup Password", win, func(pw string) {
				if err := unlockBitlocker(job.Source, pw); err != nil {
					dialog.ShowError(err, win)
					return
				}
				setJobPassword(job.Name, pw)
				runBackupWithPassword(job, pw)
			})
			return
		}

		// No BitLocker:
		// - Si RepoPassword está configurado, lo usa restic automáticamente.
		// - Si está vacío, se usa --insecure-no-password.
		runBackupWithPassword(job, "")
	})

	restoreBtn := widget.NewButton("Restore", func() {
		if selectedJobIndex < 0 || selectedJobIndex >= len(cfg.Jobs) {
			dialog.ShowInformation("Restore", "Please select a job first.", win)
			return
		}
		if selectedSnapIdx < 0 || selectedSnapIdx >= len(filterSnapshots) {
			dialog.ShowInformation("Restore", "Please select a snapshot first.", win)
			return
		}
		job := cfg.Jobs[selectedJobIndex]
		snap := filterSnapshots[selectedSnapIdx]

		// Ejecuta el restore real (ya habiendo pedido contraseña si hace falta).
		// Ejecuta el restore real (ya habiendo pedido contraseña si hace falta).
		doRestore := func() {
			go func(j Job, s Snapshot) {
				target, err := restoreSnapshot(j, cfg.ResticPath, s.ShortID, "")
				if err != nil {
					fyne.CurrentApp().SendNotification(&fyne.Notification{
						Title:   "Restore failed",
						Content: err.Error(),
					})
					dialog.ShowError(err, win)
					return
				}

				fyne.CurrentApp().SendNotification(&fyne.Notification{
					Title:   "Restore completed",
					Content: fmt.Sprintf("Snapshot %s restored to source %s", s.ShortID, target),
				})
				setStatus(fmt.Sprintf("Snapshot %s restored to %s", s.ShortID, target))

				// Volvemos a leer los snapshots reales desde restic.
				snaps, e := listSnapshots(j, cfg.ResticPath)
				if e != nil {
					if enableLogs {
						logPrintf("listSnapshots after restore failed: %v", e)
					}
					return
				}

				// Calculamos y persistimos el diff del restore contra la versión
				// cronológicamente previa (último snapshot de 'snaps').
				if _, err := computeAndStoreRestoreDiff(j, cfg.ResticPath, snaps, s.ShortID); err != nil && enableLogs {
					logPrintf("computeAndStoreRestoreDiff failed for job=%s snapshot=%s: %v", j.Name, s.ShortID, err)
				}

				// Registramos el restore en memoria con el timestamp actual.
				now := time.Now()
				markSnapshotRestored(j.Name, s.ShortID, now)

				// Reconstruimos la lista con filas virtuales para todos los restores.
				snaps = appendVirtualRestores(j, snaps)

				allSnapshots = snaps
				sortSnapshotsForCurrentJob()
				rebuildSnapFilter(snapSearch.Text)
				if snapTable != nil {
					snapTable.Refresh()
				}

			}(job, snap)
		}

		// Confirmación explícita: ahora se pisa la carpeta Source.
		confirmAndRun := func() {
			msg := fmt.Sprintf(
				"Restore snapshot %s into Source path?\n\nSource: %s\n\n"+
					"The current contents of this folder will be moved to:\n%s.before-restore-YYYYMMDDHHMMSS",
				snap.ShortID,
				job.Source,
				job.Source,
			)
			dialog.ShowConfirm("Restore", msg, func(ok bool) {
				if !ok {
					return
				}
				doRestore()
			}, win)
		}

		if job.Bitlocker {
			// Para BitLocker, primero nos aseguramos de tener contraseña cacheada.
			promptRepoPassword(job, "Enter Password", "1 Repo Password", win, func(_ string) {
				confirmAndRun()
			})
		} else {
			confirmAndRun()
		}
	})

	// live search hooks
	snapSearch.OnChanged = func(s string) {
		rebuildSnapFilter(s)
		snapTable.Refresh()
	}
	fileSearch.OnChanged = func(s string) {
		rebuildFilesFilter(s)
		filesTable.Refresh()
	}

	// assemble right panels
	snapTop := container.NewBorder(
		container.NewHBox(backupBtn, restoreBtn), nil, nil, nil,
		snapTable,
	)
	snapPanel := container.NewBorder(snapSearchRow, nil, nil, nil, snapTop)
	filesPanel := container.NewBorder(fileSearchRow, nil, nil, nil, filesTable)
	rightSplit := container.NewVSplit(snapPanel, filesPanel)
	rightSplit.Offset = 0.55

	// job selection hook at end to use created widgets
	jobList.OnSelected = func(id widget.ListItemID) {
		if int(id) < 0 || int(id) >= len(filterJobIdx) {
			selectedJobIndex = -1
			setStatus("No job selected")
			return
		}
		selectedJobIndex = filterJobIdx[id]
		selectedSnapIdx = -1
		job := cfg.Jobs[selectedJobIndex]

		loadSnaps := func() {
			setStatus("Loading snapshots…")
			prog := dialog.NewProgressInfinite("Loading snapshots", "Please wait…", win)
			prog.Show()
			go func(j Job) {
				defer prog.Hide()
				if enableLogs {
					logPrintf("[ui] load snapshots for job=%s", j.Name)
				}
				snaps, err := listSnapshots(j, cfg.ResticPath)
				if err != nil {
					dialog.ShowError(err, win)
					return
				}
				allSnapshots = snaps

				// Orden por defecto: Last check ascendente.
				snapSortColumn = SnapSortLastCheck
				snapSortAsc = true
				sortSnapshotsForCurrentJob()

				rebuildSnapFilter(snapSearch.Text)
				snapTable.Refresh()

				// Si el usuario todavía no tiene anchos personalizados,
				// calculamos autoajuste de columnas de snapshots y lo guardamos.
				if len(cfg.SnapColWidths) == 0 {
					cfg.SnapColWidths = autoSnapshotColumnWidths()
					if err := saveConfig(cfg); err != nil && enableLogs {
						logPrintf("could not save snapshot column widths: %v", err)
					}
				}
				applySnapshotColumnWidths()
				// resetear vista de archivos al cambiar de job
				allFiles = nil
				rebuildFilesFilter(fileSearch.Text)
				filesTable.Refresh()
				setStatusJob(&j)
			}(job)
		}

		// Para jobs BitLocker, primero aseguramos contraseña (cache) para restic.
		if job.Bitlocker {
			promptRepoPassword(job, "Enter Password", "Bitlocker Password", win, func(_ string) {
				loadSnaps()
			})
		} else {
			loadSnaps()
		}
	}

	// auto-select first job if available
	if len(cfg.Jobs) > 0 && len(filterJobIdx) > 0 {
		jobList.Select(0)
	}

	// top bar with about button on right
	topBar := container.NewBorder(nil, nil, nil, aboutBtn, nil)
	mainSplit := container.NewHSplit(left, rightSplit)
	mainSplit.Offset = 0.32
	root := container.NewBorder(topBar, statusLabel, nil, nil, mainSplit)
	win.SetContent(root)

	// watcher periódico: persiste config cuando cambian los anchos de columnas
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			// si ya no hay ventanas, salimos (app cerrándose)
			if len(fyne.CurrentApp().Driver().AllWindows()) == 0 {
				return
			}

			changed := false

			if snapTable != nil {
				newW := grabTableColumnWidths(snapTable, 8, cfg.SnapColWidths)
				if !float32SlicesEqual(newW, cfg.SnapColWidths) {
					cfg.SnapColWidths = newW
					changed = true
				}
			}

			if filesTable != nil {
				newW := grabTableColumnWidths(filesTable, 5, cfg.FileColWidths)
				if !float32SlicesEqual(newW, cfg.FileColWidths) {
					cfg.FileColWidths = newW
					changed = true
				}
			}

			if changed {
				if err := saveConfig(cfg); err != nil && enableLogs {
					logPrintf("could not save config on width change: %v", err)
				}
			}
		}
	}()

	// al cerrar la ventana guardamos los anchos actuales de las columnas (último flush)
	win.SetOnClosed(func() {
		if snapTable != nil {
			cfg.SnapColWidths = grabTableColumnWidths(snapTable, 8, cfg.SnapColWidths)
		}
		if filesTable != nil {
			cfg.FileColWidths = grabTableColumnWidths(filesTable, 5, cfg.FileColWidths)
		}
		if err := saveConfig(cfg); err != nil && enableLogs {
			logPrintf("could not save config on close: %v", err)
		}
	})

	setStatus("Ready")
	return win
}

// runBackupWithPassword runs a backup for the currently selected job using
// the provided password. It shows a progress dialog and on success reloads
// the snapshot list and computes the latest diff. If password is empty the
// job's RepoPassword is used.
func runBackupWithPassword(job Job, password string) {
	w := fyne.CurrentApp().Driver().AllWindows()[0]
	prog := dialog.NewProgress("Backup in progress", fmt.Sprintf("Backing up %s…", job.Name), w)
	prog.SetValue(0)
	prog.Show()

	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()

		done := make(chan error, 1)

		// goroutine que ejecuta el backup real
		go func() {
			err := doBackup(job, cfg.ResticPath, password, func(msg string) {
				if enableLogs {
					logPrintf("[backup] %s", msg)
				}
			})
			done <- err
		}()

		val := 0.0

		for {
			select {
			case err := <-done:
				// terminó el backup
				prog.SetValue(1.0)
				prog.Hide()

				if err != nil {
					// backup falló
					dialog.ShowError(err, w)
				} else {
					// backup OK
					fyne.CurrentApp().SendNotification(&fyne.Notification{
						Title:   "Backup completed",
						Content: fmt.Sprintf("Backup for %s finished.", job.Name),
					})

					// mostrar spinner de "Last check" mientras se recalcula
					setLastCheckUpdating(job.Name, true)
					if snapTable != nil {
						snapTable.Refresh()
					}

					// refrescar snapshots y last-check en segundo plano
					go func(j Job) {
						defer func() {
							// apagar spinner y refrescar la tabla cuando terminamos
							setLastCheckUpdating(j.Name, false)
							if snapTable != nil {
								snapTable.Refresh()
							}
						}()

						// 1) leer snapshots luego del backup
						snaps, e := listSnapshots(j, cfg.ResticPath)
						if e != nil {
							if enableLogs {
								logPrintf("listSnapshots after backup failed: %v", e)
							}
							return
						}

						// 2) calcular diff del último snapshot (puede borrar el último
						//    si no hay cambios y actualizar "Last check")
						if _, err2 := computeAndStoreLatestDiff(j, cfg.ResticPath, snaps); err2 != nil {
							if enableLogs {
								logPrintf("diff computation failed: %v", err2)
							}
						}

						// 3) volver a leer snapshots porque computeAndStoreLatestDiff
						//    puede haber eliminado el snapshot más nuevo
						snaps, e = listSnapshots(j, cfg.ResticPath)
						if e != nil {
							if enableLogs {
								logPrintf("listSnapshots after diff failed: %v", e)
							}
							return
						}

						// 4) actualizar la UI con la lista real + restores virtuales y el Last check recién escrito
						snaps = appendVirtualRestores(j, snaps)
						allSnapshots = snaps

						// manteniendo columna/sentido actuales
						sortSnapshotsForCurrentJob()

						// respetar el filtro actual de snapshots
						rebuildSnapFilter(filterSnapQuery)
						if snapTable != nil {
							snapTable.Refresh()
						}

					}(job)
				}

				// update live backups (in case retention settings changed)
				startLiveBackups(cfg.ResticPath)
				return

			case <-t.C:
				// animación de progreso mientras corre el backup
				val += 0.02
				if val > 0.95 {
					val = 0.95
				}
				prog.SetValue(val)
			}
		}
	}()
}

// setStatusJob updates the status bar to reflect the selected job.
func setStatusJob(job *Job) {
	if job == nil {
		setStatus("No job selected")
		return
	}
	setStatus(fmt.Sprintf("Job: %s | Source: %s | Dest: %s", job.Name, job.Source, job.Destination))
}

// setStatusSnapshot updates the status bar to reflect the selected snapshot.
func setStatusSnapshot(job *Job, s *Snapshot) {
	if job == nil {
		setStatusJob(nil)
		return
	}
	if s == nil {
		setStatusJob(job)
		return
	}
	setStatus(fmt.Sprintf("Job: %s | Snapshot: %s @ %s", job.Name, s.ShortID, s.Time.Format("02/01/2006 15:04:05")))
}

// rebuildJobFilter updates the filterJobIdx slice based on the search query.
// It is used to filter jobs by name, source or destination.
func rebuildJobFilter(q string) {
	filterJobIdx = filterJobIdx[:0]
	qq := strings.ToLower(strings.TrimSpace(q))
	for i, j := range cfg.Jobs {
		if qq == "" ||
			strings.Contains(strings.ToLower(j.Name), qq) ||
			strings.Contains(strings.ToLower(j.Source), qq) ||
			strings.Contains(strings.ToLower(j.Destination), qq) {
			filterJobIdx = append(filterJobIdx, i)
		}
	}
}

// rebuildSnapFilter updates the filtered snapshot list based on the search query.
func rebuildSnapFilter(q string) {
	filterSnapQuery = strings.ToLower(strings.TrimSpace(q))
	filterSnapshots = filterSnapshots[:0]
	for _, s := range allSnapshots {
		match := filterSnapQuery == "" ||
			strings.Contains(strings.ToLower(s.ShortID), filterSnapQuery) ||
			strings.Contains(strings.ToLower(s.Hostname), filterSnapQuery)
		if !match {
			for _, p := range s.Paths {
				if strings.Contains(strings.ToLower(p), filterSnapQuery) {
					match = true
					break
				}
			}
		}
		if match {
			filterSnapshots = append(filterSnapshots, s)
		}
	}
}

// appendVirtualRestores toma la lista "real" de snapshots de restic
// y agrega una fila "virtual" por cada snapshot restaurado en esta sesión.
// Esto evita que, después de un backup, se pierda visualmente el snapshot
// "<ID> (restored)" en la tabla de snapshots.
func appendVirtualRestores(job Job, snaps []Snapshot) []Snapshot {
	// Si no tenemos restores registrados, devolvemos tal cual.
	if restoredSnapshots == nil {
		return snaps
	}

	// Por cada snapshot real, si existe un restoreTime registrado,
	// creamos una copia "virtual" con la hora de restore y sin tags.
	for _, s := range snaps {
		if rt, ok := snapshotRestoreTime(job.Name, s.ShortID); ok {
			c := s
			c.Time = rt
			c.Tags = nil // en restores no mostramos tags
			snaps = append(snaps, c)
		}
	}
	return snaps
}

// rebuildFilesFilter updates the filtered file list based on the search query.
func rebuildFilesFilter(q string) {
	filterFileQuery = strings.ToLower(strings.TrimSpace(q))
	filterFiles = filterFiles[:0]
	for _, f := range allFiles {
		if filterFileQuery == "" ||
			strings.Contains(strings.ToLower(f.Path), filterFileQuery) ||
			strings.Contains(strings.ToLower(f.Change), filterFileQuery) {
			filterFiles = append(filterFiles, f)
		}
	}
}

// handleSnapshotHeaderClick cambia columna/sentido de orden y refresca tabla.
func handleSnapshotHeaderClick(col int) {
	var newCol SnapSortColumn

	switch col {
	case 0:
		newCol = SnapSortID
	case 1:
		newCol = SnapSortDate
	case 2:
		newCol = SnapSortTime
	case 3:
		newCol = SnapSortHost
	case 4:
		newCol = SnapSortPaths
	case 6:
		newCol = SnapSortTags
	case 7:
		newCol = SnapSortLastCheck
	default:
		// Δ u otras columnas no ordenables
		return
	}

	if snapSortColumn == newCol {
		snapSortAsc = !snapSortAsc
	} else {
		snapSortColumn = newCol
		snapSortAsc = true
	}

	sortSnapshotsForCurrentJob()
	rebuildSnapFilter(filterSnapQuery)
	if snapTable != nil {
		snapTable.Refresh()
	}
}

// handleFilesHeaderClick cambia columna/sentido de orden y refresca tabla de files.
func handleFilesHeaderClick(col int) {
	var newCol FileSortColumn

	switch col {
	case 0:
		newCol = FileSortChange
	case 1:
		newCol = FileSortPath
	case 2:
		newCol = FileSortSize
	case 3:
		newCol = FileSortMTime
	case 4:
		newCol = FileSortCTime
	default:
		return
	}

	if fileSortColumn == newCol {
		fileSortAsc = !fileSortAsc
	} else {
		fileSortColumn = newCol
		fileSortAsc = true
	}

	sortFiles()
	rebuildFilesFilter(filterFileQuery)
	if filesTable != nil {
		filesTable.Refresh()
	}
}

// float32SlicesEqual compara dos slices de float32
func float32SlicesEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// showJobEditor opens a dialog allowing the user to create or edit a job.
// When index is -1 a new job is created. When editing a job the form fields
// are pre-populated. On submit the configuration is saved and the job list
// refreshed. Live backup schedules are restarted if necessary.
func showJobEditor(parent fyne.Window, index int, jl *widget.List) {
	var job Job
	editing := false
	if index >= 0 && index < len(cfg.Jobs) {
		job = cfg.Jobs[index]
		editing = true
	}
	name := widget.NewEntry()
	name.SetText(job.Name)
	src := widget.NewEntry()
	src.SetText(job.Source)
	dest := widget.NewEntry()
	dest.SetText(job.Destination)
	pw := widget.NewPasswordEntry()
	pw.SetText(job.RepoPassword)
	pw.SetPlaceHolder("Backup password (optional)")
	bitlockerCheck := widget.NewCheck("BitLocker", nil)
	bitlockerCheck.SetChecked(job.Bitlocker)
	liveCheck := widget.NewCheck("Live backup", nil)
	liveCheck.SetChecked(job.Live)
	ival := widget.NewEntry()
	if job.Interval > 0 {
		ival.SetText(strconv.Itoa(job.Interval))
	}
	keep := widget.NewEntry()
	if job.KeepLast > 0 {
		keep.SetText(strconv.Itoa(job.KeepLast))
	}
	unit := widget.NewSelect([]string{"seconds", "minutes", "hours", "days"}, nil)
	if job.IntervalUnit != "" {
		unit.SetSelected(job.IntervalUnit)
	} else {
		unit.SetSelected("minutes")
	}
	browseSrc := widget.NewButton("Browse", func() {
		openFolderDialog("Select source", func(p string) { src.SetText(p) })
	})
	browseDst := widget.NewButton("Browse", func() {
		openFolderDialog("Select destination", func(p string) { dest.SetText(p) })
	})

	// FormItems para poder manipular el de password
	pwItem := &widget.FormItem{Text: "3 Repo password", Widget: pw}

	var w fyne.Window
	form := &widget.Form{
		Items: []*widget.FormItem{
			{Text: "Name", Widget: name},
			{Text: "Source", Widget: container.NewBorder(nil, nil, nil, browseSrc, src)},
			{Text: "Destination", Widget: container.NewBorder(nil, nil, nil, browseDst, dest)},
			pwItem,
			{Text: "BitLocker", Widget: bitlockerCheck},
			{Text: "Live backup", Widget: liveCheck},
			{Text: "Interval", Widget: container.NewBorder(nil, nil, unit, nil, ival)},
			{Text: "Retention (keep last N)", Widget: keep},
		},
		OnSubmit: func() {
			if strings.TrimSpace(name.Text) == "" || strings.TrimSpace(src.Text) == "" || strings.TrimSpace(dest.Text) == "" {
				dialog.ShowError(errors.New("name, source and destination are required"), w)
				return
			}
			iv := 0
			if ival.Text != "" {
				iv, _ = strconv.Atoi(ival.Text)
			}
			kl := 0
			if keep.Text != "" {
				kl, _ = strconv.Atoi(keep.Text)
			}

			repoPwd := pw.Text
			if bitlockerCheck.Checked {
				// No persistimos contraseña cuando se usa BitLocker.
				repoPwd = ""
			}

			n := Job{
				Name:         name.Text,
				Source:       src.Text,
				Destination:  dest.Text,
				RepoPassword: repoPwd,
				Bitlocker:    bitlockerCheck.Checked,
				Live:         liveCheck.Checked,
				Interval:     iv,
				IntervalUnit: unit.Selected,
				KeepLast:     kl,
			}
			if editing {
				cfg.Jobs[index] = n
				// Al editar, limpiamos cualquier password cacheada anterior.
				clearJobPassword(n.Name)
			} else {
				cfg.Jobs = append(cfg.Jobs, n)
				// recordar índice del nuevo job en el slice global
				selectedJobIndex = len(cfg.Jobs) - 1
			}
			if err := saveConfig(cfg); err != nil {
				dialog.ShowError(err, w)
				return
			}
			// rebuild job filter and refresh list
			rebuildJobFilter(jobSearch.Text)
			jl.Refresh()
			// Si se añadió un job nuevo, seleccionarlo en la vista filtrada
			if !editing {
				for row, idx2 := range filterJobIdx {
					if idx2 == selectedJobIndex {
						jl.Select(row)
						break
					}
				}
			}
			// restart live backups
			startLiveBackups(cfg.ResticPath)
			w.Close()
		},
		OnCancel: func() { w.Close() },
	}
	form.SubmitText = "Save"
	form.CancelText = "Cancel"

	// Callback para ocultar/mostrar el campo de password según BitLocker.
	bitlockerCheck.OnChanged = func(checked bool) {
		if checked {
			pw.SetText("")
			pw.Hide()
		} else {
			pw.Show()
		}
	}
	// aplicar estado inicial
	bitlockerCheck.OnChanged(bitlockerCheck.Checked)

	w = fyne.CurrentApp().NewWindow("Job Editor")
	w.SetContent(container.NewVScroll(form))
	w.Resize(fyne.NewSize(600, 420))
	w.Show()
}

// showSettings presents a form to configure the restic executable location
// and the autostart option. Changes are persisted immediately on submit.
// showSettings presents a form to configure the restic executable location
// and the autostart option. Changes are persisted immediately on submit.
//
// Además muestra el estado de actualización (último control) y un botón
// para chequear/actualizar.
func showSettings(parent fyne.Window) {
	resticEntry := widget.NewEntry()
	resticEntry.SetText(cfg.ResticPath)

	// Checkbox que controla si shed se registra o no para iniciar con el sistema.
	autoStartCheck := widget.NewCheck("Launch on startup", func(bool) {})
	autoStartCheck.SetChecked(cfg.AutoStart)

	browse := widget.NewButton("Browse", func() {
		openFileDialog("Select restic executable", func(p string) { resticEntry.SetText(p) })
	})

	// ---- Sección de actualización
	statusText := cfg.LastUpdateStatus
	if statusText == "" {
		statusText = "Never checked"
	}
	statusLabel := widget.NewLabel(statusText)

	lastCheckStr := "Never"
	if !cfg.LastUpdateCheck.IsZero() {
		lastCheckStr = cfg.LastUpdateCheck.Format("02/01/2006 15:04:05")
	}
	lastCheckLabel := widget.NewLabel(lastCheckStr)

	checkBtn := widget.NewButton("Check for updates", func() {
		CheckForUpdatesDialog(parent)
		// El callback actualiza cfg y muestra diálogos; la próxima vez que se
		// abra Settings se verán los nuevos valores. No refrescamos en vivo
		// aquí para mantenerlo simple.
	})

	updateBox := container.NewVBox(
		statusLabel,
		lastCheckLabel,
		checkBtn,
	)

	var w fyne.Window
	form := &widget.Form{
		Items: []*widget.FormItem{
			{
				Text: "Restic executable",
				Widget: container.NewBorder(nil, nil, nil, browse,
					resticEntry),
			},
			{Text: "Autostart", Widget: autoStartCheck},
			{Text: "Updates", Widget: updateBox},
		},
		OnSubmit: func() {
			// Validación de la ruta de restic.
			p := strings.TrimSpace(resticEntry.Text)
			if p == "" {
				dialog.ShowError(errors.New("select restic executable"), w)
				return
			}
			abs, err := filepath.Abs(p)
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			if fi, err := os.Stat(abs); err != nil || fi.IsDir() {
				dialog.ShowError(errors.New("invalid restic path"), w)
				return
			}

			// Persistimos configuración.
			cfg.ResticPath = abs
			cfg.AutoStart = autoStartCheck.Checked
			if err := saveConfig(cfg); err != nil {
				dialog.ShowError(err, w)
				return
			}

			// Según el estado del checkbox, instalamos o eliminamos autostart.
			if cfg.AutoStart {
				if err := installAutostart(); err != nil && enableLogs {
					logPrintf("autostart install failed: %v", err)
				}
			} else {
				if err := uninstallAutostart(); err != nil && enableLogs {
					logPrintf("autostart uninstall failed: %v", err)
				}
			}

			// Reiniciamos live backups con la nueva ruta de restic.
			startLiveBackups(cfg.ResticPath)
			w.Close()
		},
		OnCancel: func() { w.Close() },
	}
	form.SubmitText = "Save"
	form.CancelText = "Cancel"

	w = fyne.CurrentApp().NewWindow("Settings")
	w.SetContent(container.NewVScroll(form))
	w.Resize(fyne.NewSize(560, 260))
	w.Show()
}
