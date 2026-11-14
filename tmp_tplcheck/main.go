package main

import (
	"fmt"
	"html/template"
	"io/fs"
	"os"
)

func main() {
	funcMap := template.FuncMap{"i18n": func(key string, params ...string) string { return key }}
	t := template.New("").Funcs(funcMap)
	fsys := os.DirFS("web")
	_ = fs.WalkDir(fsys, "html", func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if nt, e := t.ParseFS(fsys, path+"/*.html"); e == nil {
			t = nt
		}
		return nil
	})
	data := map[string]interface{}{"title": "t", "host": "127.0.0.1", "request_uri": "/dmud/panel/inbounds", "base_path": "/dmud/", "cur_ver": "2.77.0"}
	f, _ := os.Create(`G:\Temp\h\page.html`)
	defer f.Close()
	if err := t.Lookup("inbounds.html").Execute(f, data); err != nil {
		fmt.Println("EXEC-ERR:", err)
	}
	fmt.Println("rendered ok")
}
