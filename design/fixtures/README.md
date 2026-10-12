# Fixtures de desarrollo

Estos ejemplos son sintéticos; no contienen configuración ni mensajes reales de la estación.

- `linmail.cfg` usa contraseñas ficticias (`sysop-pass`, `member-pass`) para los tests.
- `Mail/*.mes` cubre texto con rutas `R:`, un mensaje RFC822 y un B2 MIME con adjunto base64.
- `HTML/7plus/` tiene un archivo de texto de demostración.
- Los tests construyen DIRMES.SYS en memoria y copian la configuración a `t.TempDir()` cuando necesitan escribir.

Tratar esta carpeta como read-only. No usarla como directorio operativo de la BBS ni como destino de envíos.
