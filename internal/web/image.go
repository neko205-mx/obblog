package web

import (
	"errors"
	"io"
	"io/fs"
	"log"
	"mime"
	"net/http"

	"obblog/internal/vault"
)

func (a *App) uploadImage(w http.ResponseWriter, r *http.Request) {
	if !a.writable {
		fail(w, http.StatusForbidden, "当前为只读模式")
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "multipart/form-data" {
		fail(w, http.StatusUnsupportedMediaType, "请使用 multipart/form-data 上传图片")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, vault.MaxImageSize+64*1024)
	err = r.ParseMultipartForm(1 << 20)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			fail(w, http.StatusRequestEntityTooLarge, "图片超过 20 MiB")
		} else {
			fail(w, http.StatusBadRequest, "无效的图片上传请求")
		}
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) != 1 || len(r.MultipartForm.File) != 1 {
		fail(w, http.StatusBadRequest, "每次上传一张图片")
		return
	}
	f, err := files[0].Open()
	if err != nil {
		fail(w, http.StatusBadRequest, "无法读取上传图片")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, vault.MaxImageSize+1))
	if err != nil {
		fail(w, http.StatusBadRequest, "图片上传未完成")
		return
	}
	name, err := a.vault.ImageName(files[0].Filename, data)
	var saved any
	if err == nil {
		write := func() (*vault.SavedNote, error) { return a.vault.SaveImage(name, data) }
		if a.git != nil {
			saved, err = a.git.Save(r.Context(), r.FormValue("gitHead"), name, write)
		} else {
			saved, err = write()
		}
	}
	if err != nil {
		switch {
		case errors.Is(err, vault.ErrImageTooLarge):
			fail(w, http.StatusRequestEntityTooLarge, "图片超过 20 MiB")
		case errors.Is(err, vault.ErrInvalidImage):
			fail(w, http.StatusUnsupportedMediaType, "支持 PNG、JPEG、GIF、WebP、AVIF 和 SVG 图片")
		case errors.Is(err, vault.ErrConflict):
			fail(w, http.StatusConflict, "同名图片刚被创建，请重试上传")
		case errors.Is(err, fs.ErrPermission):
			fail(w, http.StatusForbidden, "知识库目录没有写入权限")
		default:
			if a.git != nil {
				fail(w, http.StatusConflict, err.Error())
			} else {
				log.Printf("upload image: %v", err)
				fail(w, http.StatusInternalServerError, "图片保存失败")
			}
		}
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusCreated)
	respond(w, saved)
}
