package main

import (
	"reflect"

	"fyne.io/fyne/v2/widget"
)

// grabTableColumnWidths lee, mediante reflexión, el mapa interno de anchos de
// columnas de widget.Table. Devuelve un slice de longitud colCount con los
// anchos actuales. Para columnas sin valor en el mapa se usa fallback (si
// tiene ese índice) o 0. Esta función depende de detalles internos de Fyne.
func grabTableColumnWidths(t *widget.Table, colCount int, fallback []float32) []float32 {
	widths := make([]float32, colCount)

	// copiar fallback si está disponible
	if len(fallback) >= colCount {
		copy(widths, fallback[:colCount])
	}

	v := reflect.ValueOf(t)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return widths
	}
	v = v.Elem()

	field := v.FieldByName("columnWidths")
	if !field.IsValid() || field.IsZero() || field.Kind() != reflect.Map {
		return widths
	}

	for _, key := range field.MapKeys() {
		if key.Kind() != reflect.Int {
			continue
		}
		col := int(key.Int())
		if col < 0 || col >= colCount {
			continue
		}
		val := field.MapIndex(key)
		if !val.IsValid() {
			continue
		}
		if val.Kind() != reflect.Float32 && val.Kind() != reflect.Float64 {
			continue
		}
		widths[col] = float32(val.Float())
	}

	return widths
}
