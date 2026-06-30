package main

import (
	"database/sql"
	"errors"
	"net/http"

	qrcode "urlshortener/qrcode"
)

func (s *server) handleLinkQRCode(w http.ResponseWriter, r *http.Request) {
	uid := userIDFromCtx(r.Context())
	id := atoiOr(r.PathValue("id"), -1)

	link, err := s.fetchLink(uid, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "link not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not load link")
		return
	}

	png, err := qrcode.Encode(link.ShortURL, qrcode.High, 512)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not generate qr code")
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Content-Disposition", `attachment; filename="`+link.ShortCode+`.png"`)
	w.Write(png)
}
