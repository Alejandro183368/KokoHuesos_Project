package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	_ "github.com/mattn/go-sqlite3"
)

var db *sql.DB

type ItemOrden struct {
	ID       int    `json:"id"`
	Cantidad int    `json:"cantidad"`
	Nombre   string `json:"nombre"`
	Tamano   string `json:"tamano"`
}

func main() {
	initDB()
	setupDatosPrueba()
	defer db.Close()

	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	e.File("/", "public/index.html")
	e.File("/mesero", "public/mesero.html")
	e.File("/barra", "public/barra.html")
	e.File("/admin", "public/admin.html")
	e.Static("/", "public")
	e.GET("/productos", handleGetProductos)
	e.POST("/venta/registrar-comanda", handleRegistrarComanda)
	e.GET("/barra/pedidos", handleGetPedidos)
	e.POST("/pedido/completar-orden/:id", handleCompletarOrden)

	e.GET("/admin/mesas", handleEstadoMesas)
	e.GET("/admin/recibo/:mesa", handleGenerarRecibo)
	e.POST("/admin/pagar-mesa/:mesa", handlePagarMesa)
	e.GET("/admin/reporte", handleReporteHistorico)
	e.POST("/admin/finalizar-dia", handleFinalizarDia)

	e.GET("/admin/inventario", handleVerInventario)
	e.POST("/admin/inventario/recargar", handleRecargarInventario)

	fmt.Println("🍺 KOKOHuesos — Sistema iniciado en http://localhost:8080")
	e.Logger.Fatal(e.Start(":8080"))
}

func initDB() {
	var err error
	db, err = sql.Open("sqlite3", "./bar.db?_foreign_keys=on")
	if err != nil {
		panic(err)
	}

	schema := `
	CREATE TABLE IF NOT EXISTS categorias (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		nombre TEXT UNIQUE,
		orden INTEGER DEFAULT 0
	);
	CREATE TABLE IF NOT EXISTS insumos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		nombre TEXT UNIQUE,
		cantidad REAL DEFAULT 0,
		unidad TEXT,
		alerta_minima REAL DEFAULT 5,
		categoria TEXT DEFAULT 'General'
	);
	CREATE TABLE IF NOT EXISTS productos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		nombre TEXT UNIQUE,
		precio_medio REAL DEFAULT 0,
		precio_litro REAL DEFAULT 0,
		categoria_id INTEGER,
		activo INTEGER DEFAULT 1
	);
	CREATE TABLE IF NOT EXISTS pedidos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		orden_id TEXT,
		producto_id INTEGER,
		mesa TEXT DEFAULT 'Barra',
		tamano TEXT DEFAULT 'medio',
		precio REAL DEFAULT 0,
		estado TEXT DEFAULT 'pendiente', 
		fecha DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	db.Exec(schema)
}

func handleGetProductos(c echo.Context) error {
	rows, err := db.Query(`
		SELECT p.id, p.nombre, p.precio_medio, p.precio_litro, c.nombre as categoria
		FROM productos p
		JOIN categorias c ON p.categoria_id = c.id
		WHERE p.activo = 1
		ORDER BY c.orden, p.nombre`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type Producto struct {
		ID          int     `json:"id"`
		Nombre      string  `json:"nombre"`
		PrecioMedio float64 `json:"precio_medio"`
		PrecioLitro float64 `json:"precio_litro"`
		Categoria   string  `json:"categoria"`
	}

	productos := []Producto{}
	for rows.Next() {
		var p Producto
		rows.Scan(&p.ID, &p.Nombre, &p.PrecioMedio, &p.PrecioLitro, &p.Categoria)
		productos = append(productos, p)
	}
	return c.JSON(http.StatusOK, productos)
}

func handleRegistrarComanda(c echo.Context) error {
	mesa := c.FormValue("mesa")
	itemsJSON := c.FormValue("items")
	var items []ItemOrden
	json.Unmarshal([]byte(itemsJSON), &items)

	if len(items) == 0 {
		return c.HTML(http.StatusBadRequest, "No hay items")
	}

	var ordenID string
	err := db.QueryRow("SELECT orden_id FROM pedidos WHERE mesa = ? AND estado != 'pagado' LIMIT 1", mesa).Scan(&ordenID)
	if err != nil {
		ordenID = fmt.Sprintf("%s-%d", mesa, time.Now().Unix())
	}

	tx, _ := db.Begin()
	for _, item := range items {
		var precio float64
		col := "precio_medio"
		if item.Tamano == "litro" {
			col = "precio_litro"
		}
		db.QueryRow(fmt.Sprintf("SELECT %s FROM productos WHERE id = ?", col), item.ID).Scan(&precio)
		for i := 0; i < item.Cantidad; i++ {
			tx.Exec("INSERT INTO pedidos (producto_id, mesa, orden_id, tamano, precio, estado) VALUES (?, ?, ?, ?, ?, 'pendiente')",
				item.ID, mesa, ordenID, item.Tamano, precio)
		}
	}
	tx.Commit()
	return c.HTML(http.StatusOK, "✔ Orden enviada")
}

func handleGetPedidos(c echo.Context) error {
	rows, err := db.Query(`
		SELECT p.orden_id, p.mesa, pr.nombre, p.tamano, COUNT(*) as qty
		FROM pedidos p
		JOIN productos pr ON p.producto_id = pr.id
		WHERE p.estado = 'pendiente'
		GROUP BY p.orden_id, pr.nombre, p.tamano
		ORDER BY p.fecha ASC`)
	if err != nil {
		return c.HTML(500, "Error DB")
	}
	defer rows.Close()

	type Comanda struct {
		Mesa  string
		Items []string
	}
	comandas := make(map[string]*Comanda)
	var keys []string

	for rows.Next() {
		var oid, mesa, nom, tam string
		var qty int
		rows.Scan(&oid, &mesa, &nom, &tam, &qty)
		if _, ok := comandas[oid]; !ok {
			comandas[oid] = &Comanda{Mesa: mesa, Items: []string{}}
			keys = append(keys, oid)
		}
		label := "½ L"
		if tam == "litro" {
			label = "1 L"
		}
		itemHtml := fmt.Sprintf(`
			<div style="display:flex; justify-content:space-between; align-items:center; border-bottom:1px solid #eee; padding:8px 0;">
				<span style="font-weight:600;">%dx %s</span>
				<span style="background:#f39c12; color:white; padding:2px 8px; border-radius:12px; font-size:0.8em;">%s</span>
			</div>`, qty, nom, label)
		comandas[oid].Items = append(comandas[oid].Items, itemHtml)
	}

	html := ""
	for _, k := range keys {
		html += fmt.Sprintf(`
			<div style="background:white; border-radius:15px; box-shadow:0 10px 25px rgba(0,0,0,0.1); width:300px; margin:10px; overflow:hidden; font-family:'Barlow', sans-serif;">
				<div style="background:#0d1b0f; color:#f9ca24; padding:15px; text-align:center; font-family:'Bebas Neue'; font-size:1.5em; letter-spacing:2px;">
					MESA %s
				</div>
				<div style="padding:20px; color:#333;">
					%s
				</div>
				<button hx-post="/pedido/completar-orden/%s" hx-target="closest div" hx-swap="outerHTML"
					style="width:100%%; padding:15px; background:#27ae60; color:white; border:none; cursor:pointer; font-weight:bold; font-size:1.1em;">
					✅ LISTO
				</button>
			</div>`, comandas[k].Mesa, strings.Join(comandas[k].Items, ""), k)
	}

	if html == "" {
		html = "<div style='color:white; opacity:0.5; padding:50px; text-align:center; width:100%%;'>☕ Sin pedidos pendientes...</div>"
	}
	return c.HTML(http.StatusOK, html)
}

func handleCompletarOrden(c echo.Context) error {
	db.Exec("UPDATE pedidos SET estado = 'servido' WHERE orden_id = ?", c.Param("id"))
	return c.HTML(http.StatusOK, "")
}

func handleGenerarRecibo(c echo.Context) error {
	mesa := c.Param("mesa")

	rows, err := db.Query(`
		SELECT pr.nombre, p.tamano, p.precio, COUNT(*) as qty
		FROM pedidos p
		JOIN productos pr ON p.producto_id = pr.id
		WHERE p.mesa = ? AND p.estado = 'servido'
		GROUP BY pr.nombre, p.tamano, p.precio`, mesa)

	if err != nil {
		return c.String(http.StatusInternalServerError, "Error al consultar la cuenta")
	}
	defer rows.Close()

	var total float64
	now := time.Now().Format("02/01/2006 15:04")

	detalle := `<div id="ticket-imprimible" style="font-family:monospace; width:280px; margin:auto;">`
	detalle += fmt.Sprintf(`
		<center>
			<strong>🦴 KOKOHUESOS 🦴</strong><br>
			Ciudad Valles, S.L.P.<br>
			Mesa: %s<br>
			<small>%s</small>
		</center>
		<hr>`, mesa, now)

	encontrado := false

	for rows.Next() {
		encontrado = true

		var n, tam string
		var p float64
		var q int

		if err := rows.Scan(&n, &tam, &p, &q); err != nil {
			return c.String(http.StatusInternalServerError, "Error al leer datos")
		}

		sub := p * float64(q)
		total += sub

		tamLabel := "½L"
		if tam == "litro" {
			tamLabel = "1L"
		}

		detalle += fmt.Sprintf(`
			<div style="display:flex; justify-content:space-between;">
				<span>%dx %s (%s)</span>
				<span>$%.2f</span>
			</div>`, q, n, tamLabel, sub)
	}

	if !encontrado {
		return c.HTML(http.StatusOK, "<p>No hay pedidos</p>")
	}

	detalle += fmt.Sprintf(`
		<hr>
		<div style="display:flex; justify-content:space-between;">
			<strong>TOTAL:</strong>
			<strong>$%.2f</strong>
		</div>
	</div>`, total)

	// 🔥 Script que SOLO imprime el ticket
	detalle += `
	<style>
	@media print {
		.no-print {
			display: none;
		}
	}
	</style>

	<div class="no-print" style="text-align:center; margin-top:10px;">
		<button onclick="imprimirTicket()" style="padding:10px;">🖨️ Imprimir</button>

		<button 
			hx-post="/admin/pagar-mesa/` + mesa + `" 
			hx-target="#visor"
			style="padding:10px; background:green; color:white;">
			💰 Cobrar
		</button>
	</div>

	<script>
	function imprimirTicket() {
		const contenido = document.getElementById("ticket-imprimible").innerHTML;

		const ventana = window.open('', '', 'width=300,height=600');

		ventana.document.write(
			"<html>" +
			"<head>" +
			"<title>Ticket</title>" +
			"<style>" +
			"body { font-family: monospace; width: 280px; margin: 0; padding: 10px; }" +
			"hr { border: none; border-top: 1px dashed black; }" +
			"</style>" +
			"</head>" +
			"<body>" +
			contenido +
			"</body>" +
			"</html>"
		);

		ventana.document.close();

		ventana.onload = function() {
			ventana.focus();
			ventana.print();
			ventana.close();
		};
	}
	</script>
	`

	return c.HTML(http.StatusOK, detalle)
}

func handlePagarMesa(c echo.Context) error {
	db.Exec("UPDATE pedidos SET estado = 'pagado' WHERE mesa = ? AND estado = 'servido'", c.Param("mesa"))
	return c.HTML(http.StatusOK, "Mesa Liberada")
}

func handleReporteHistorico(c echo.Context) error {
	periodo := c.QueryParam("periodo")
	titulo := "Ventas de Hoy"
	filtro := "date(p.fecha) = date('now')"

	if periodo == "semana" {
		filtro = "p.fecha >= date('now', '-7 days')"
		titulo = "Últimos 7 Días"
	} else if periodo == "mes" {
		filtro = "p.fecha >= date('now', 'start of month')"
		titulo = "Este Mes"
	}

	rows, _ := db.Query(`
		SELECT pr.nombre, COUNT(*) as qty, SUM(p.precio) as total
		FROM pedidos p
		JOIN productos pr ON p.producto_id = pr.id
		WHERE p.estado = 'pagado' AND ` + filtro + `
		GROUP BY pr.nombre`)
	defer rows.Close()

	var granTotal float64
	html := fmt.Sprintf("<div style='background:#1A1B1E;padding:15px;border-radius:8px;'><h4>📊 %s</h4><table style='width:100%%; border-collapse:collapse;'>", titulo)

	for rows.Next() {
		var n string
		var q int
		var s float64
		rows.Scan(&n, &q, &s)
		granTotal += s
		html += fmt.Sprintf("<tr style='border-bottom:1px solid #eee;'><td style='padding:5px;'>%dx %s</td><td style='text-align:right;'>$%.2f</td></tr>", q, n, s)
	}
	html += fmt.Sprintf("</table><h3 style='text-align:right; border-top:2px solid #333; padding-top:10px;'>TOTAL: $%.2f</h3></div>", granTotal)

	return c.HTML(http.StatusOK, html)
}

func handleEstadoMesas(c echo.Context) error {
	rows, _ := db.Query("SELECT mesa, COUNT(*) FROM pedidos WHERE estado != 'pagado' GROUP BY mesa")
	defer rows.Close()
	mesas := make(map[string]int)
	for rows.Next() {
		var m string
		var q int
		rows.Scan(&m, &q)
		mesas[m] = q
	}
	return c.JSON(http.StatusOK, mesas)
}

func handleVerInventario(c echo.Context) error {
	rows, err := db.Query("SELECT id, nombre, cantidad, unidad, alerta_minima FROM insumos ORDER BY categoria, nombre")
	if err != nil {
		return err
	}
	defer rows.Close()

	html := `<table style="width:100%; border-collapse:collapse; background:#202124;">
		<tr style="background:#1A1B1E; color:#fff5f5;">
			<th style="padding:10px; text-align:left;">Insumo</th>
			<th style="padding:10px; text-align:center;">Stock</th>
			<th style="padding:10px; text-align:center;">Estado</th>
		</tr>`

	for rows.Next() {
		var id int
		var n, u string
		var q, a float64
		rows.Scan(&id, &n, &q, &u, &a)
		color := "#27ae60"
		if q <= a {
			color = "#e74c3c"
		}
		estado := "✅ OK"
		if q <= a {
			estado = "🚨 BAJO"
		}

		html += fmt.Sprintf(`
			<tr style="border-bottom:1px solid #ddd;">
				<td style="padding:10px;">%s</td>
				<td style="padding:10px; text-align:center; font-weight:bold;">%.0f %s</td>
				<td style="padding:10px; text-align:center; color:%s; font-weight:bold;">%s</td>
			</tr>`, n, q, u, color, estado)
	}
	return c.HTML(http.StatusOK, html+"</table>")
}

func handleRecargarInventario(c echo.Context) error {
	id := c.FormValue("insumo_id")
	cantidad := c.FormValue("cantidad")

	if id == "" || cantidad == "" {
		return c.HTML(http.StatusBadRequest, "⚠️ Datos incompletos")
	}

	_, err := db.Exec("UPDATE insumos SET cantidad = cantidad + ? WHERE id = ?", cantidad, id)
	if err != nil {
		return c.HTML(http.StatusInternalServerError, "❌ Error al actualizar")
	}
	return handleVerInventario(c)
}

func handleFinalizarDia(c echo.Context) error {
	db.Exec("UPDATE pedidos SET estado = 'pagado' WHERE estado = 'servido'")
	return c.HTML(200, "Cierre de turno completado")
}

func setupDatosPrueba() {
	db.Exec(`INSERT OR IGNORE INTO categorias (id, nombre, orden) VALUES 
		(1, 'Cócteles Clásicos', 1), (2, 'Tragos Tropicales', 2), (3, 'Especialidades', 3), 
		(4, 'Brandys', 4), (5, 'Rones', 5), (6, 'Vodkas', 6), (7, 'Tequilas', 7), 
		(8, 'Whiskys', 8), (9, 'Cervezas', 9)`)

	insumos := []struct {
		id      int
		n, u, c string
	}{
		{1, "Cerveza Corona", "botellas", "Cervezas"}, {2, "Cerveza Modelo", "botellas", "Cervezas"},
		{3, "Cerveza Pacifico", "botellas", "Cervezas"}, {4, "Mezclador", "latas", "Refrescos"},
		{5, "Clamato", "latas", "Refrescos"}, {6, "Sal/Limón/Chile", "porciones", "Varios"},
		{7, "Tequila Centenario", "botellas", "Destilados"}, {8, "Tequila Tradicional", "botellas", "Destilados"},
		{9, "Tequila Cristalino", "botellas", "Destilados"}, {10, "Herradura Plata", "botellas", "Destilados"},
		{11, "Maestro Dobel", "botellas", "Destilados"}, {12, "Don Julio 70", "botellas", "Destilados"},
		{13, "Vodka Absolut", "botellas", "Destilados"}, {14, "Vodka Stoli", "botellas", "Destilados"},
		{15, "Torres 5", "botellas", "Destilados"}, {16, "Torres 10", "botellas", "Destilados"},
		{17, "Ron Flor de Caña", "botellas", "Destilados"}, {18, "Ron Appleton Estate", "botellas", "Destilados"},
		{19, "Ron Bacardi Blanco", "botellas", "Destilados"}, {20, "Ron Potosí Añejo", "botellas", "Destilados"},
		{21, "Ron Potosí Extra Añejo", "botellas", "Destilados"}, {22, "Whisky Buchanans", "botellas", "Destilados"},
		{23, "Whisky Red Label", "botellas", "Destilados"}, {24, "Whisky Black Label", "botellas", "Destilados"},
		{25, "Jack Daniels", "botellas", "Destilados"}, {26, "Whisky Bailes", "botellas", "Destilados"},
		{27, "Jugo de Piña", "litros", "Jugos"}, {28, "Jugo de Fresa", "litros", "Jugos"},
		{29, "Hielo", "bolsas", "Varios"}, {30, "Vasos/Copas", "piezas", "Varios"},
	}
	for _, i := range insumos {
		db.Exec(`INSERT OR IGNORE INTO insumos (id, nombre, cantidad, unidad, alerta_minima, categoria) 
                 VALUES (?, ?, 0, ?, 5, ?)`, i.id, i.n, i.u, i.c)
	}

	productos := []struct {
		nom    string
		cat    int
		pm, pl float64
	}{
		{"Vampiro", 1, 90, 170}, {"Clamato", 1, 90, 170}, {"Tom Collins", 1, 90, 170},
		{"Piña Colada", 2, 90, 170}, {"Fresa Colada", 2, 90, 170}, {"Daiquiri", 2, 90, 170},
		{"Michelada Original", 3, 50, 90}, {"Pócima", 3, 90, 170}, {"Koko Huesos", 3, 90, 170},
		{"Torres 5", 4, 90, 170}, {"Torres 10", 4, 100, 180},
		{"Flor de Caña", 5, 90, 170}, {"Bacardi Blanco", 5, 90, 170},
		{"Absolut", 6, 90, 170}, {"Stoli", 6, 100, 180},
		{"Centenario", 7, 90, 170}, {"Tradicional", 7, 100, 180}, {"Don Julio 70", 7, 130, 250},
		{"Buchanans", 8, 100, 200}, {"Jack Daniel's", 8, 100, 200},
	}
	for _, p := range productos {
		db.Exec(`INSERT OR IGNORE INTO productos (nombre, precio_medio, precio_litro, categoria_id, activo) 
			VALUES (?, ?, ?, ?, 1)`, p.nom, p.pm, p.pl, p.cat)
	}
}
