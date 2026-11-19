// Rating charts: the global rating of teams over time, as SVG, with a
// crosshair and a tooltip. No dependencies.
//
// ratingChart(container, series, options)
//   series: [{team, points: [{t: unix seconds, r: rating, v: version}]}], best first
//   options.highlight: how many series (the first ones) get their own color;
//     the others are drawn in gray, as context
//   options.versions: mark the start of each version
//   options.height: in pixels
// Elements of the page with a data-team attribute highlight their team's
// line when hovered.

(function () {
	"use strict";
	const SVG = "http://www.w3.org/2000/svg";
	const margin = { top: 12, right: 16, bottom: 26, left: 40 };

	function el(name, attrs, parent) {
		const node = document.createElementNS(SVG, name);
		for (const [k, v] of Object.entries(attrs || {})) node.setAttribute(k, v);
		if (parent) parent.appendChild(node);
		return node;
	}

	// About `count` round ticks between lo and hi
	function niceTicks(lo, hi, count) {
		const span = hi - lo || 1;
		const raw = span / count;
		const mag = Math.pow(10, Math.floor(Math.log10(raw)));
		const step = [1, 2, 5, 10].map(m => m * mag).find(s => s >= raw);
		const ticks = [];
		for (let v = Math.ceil(lo / step) * step; v <= hi + 1e-9; v += step) ticks.push(+v.toFixed(10));
		return ticks;
	}

	function timeTicks(t0, t1, count) {
		const span = t1 - t0;
		const steps = [3600, 3 * 3600, 6 * 3600, 12 * 3600, 86400, 2 * 86400, 7 * 86400, 14 * 86400, 30 * 86400];
		const step = steps.find(s => span / s <= count) || steps[steps.length - 1];
		const offset = new Date(t0 * 1000).getTimezoneOffset() * 60;
		const ticks = [];
		for (let t = Math.ceil((t0 - offset) / step) * step + offset; t <= t1; t += step) ticks.push(t);
		return { ticks, daily: step >= 86400 };
	}

	const dateFormat = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" });
	const hourFormat = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit" });
	const fullFormat = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });

	// The rating of a series at time t: its last point at or before t
	function valueAt(points, t) {
		let lo = 0, hi = points.length - 1, found = -1;
		while (lo <= hi) {
			const mid = (lo + hi) >> 1;
			if (points[mid].t <= t) { found = mid; lo = mid + 1; } else hi = mid - 1;
		}
		return found < 0 ? null : points[found].r;
	}

	window.ratingChart = function (container, series, options) {
		options = options || {};
		const highlight = options.highlight || 0;
		const height = options.height || 280;
		series = series.filter(s => s.points.length > 0);
		if (series.length === 0) {
			container.textContent = "No matches yet.";
			return;
		}
		series.forEach((s, i) => { s.slot = i < highlight ? i + 1 : 0; });

		container.classList.add("chart");
		container.textContent = "";
		// Legend: every colored series, and the gray ones as a group
		if (series.length > 1) {
			const legend = document.createElement("div");
			legend.className = "chart-legend";
			for (const s of series.filter(s => s.slot)) {
				const item = document.createElement("span");
				const key = document.createElement("i");
				key.className = "key series-" + s.slot;
				item.append(key, document.createTextNode(s.team));
				legend.append(item);
			}
			if (series.some(s => !s.slot)) {
				const item = document.createElement("span");
				const key = document.createElement("i");
				key.className = "key context";
				item.append(key, document.createTextNode(highlight ? "other teams" : "teams"));
				legend.append(item);
			}
			container.append(legend);
		}
		const frame = document.createElement("div");
		frame.className = "chart-frame";
		container.append(frame);
		const tooltip = document.createElement("div");
		tooltip.className = "chart-tooltip";
		tooltip.hidden = true;
		frame.append(tooltip);

		const times = [...new Set(series.flatMap(s => s.points.map(p => p.t)))].sort((a, b) => a - b);
		const t0 = times[0], t1 = Math.max(times[times.length - 1], t0 + 3600);
		const values = series.flatMap(s => s.points.map(p => p.r));
		const ticks = niceTicks(Math.min(0, ...values), Math.max(...values), 5);
		const y0 = Math.min(ticks[0], ...values), y1 = Math.max(ticks[ticks.length - 1], ...values);

		let svg, x, y, cross, focused = null;
		const lines = new Map();

		function draw() {
			const width = Math.max(frame.clientWidth, 300);
			x = t => margin.left + (t - t0) / (t1 - t0) * (width - margin.left - margin.right);
			y = r => margin.top + (y1 - r) / (y1 - y0 || 1) * (height - margin.top - margin.bottom);
			if (svg) svg.remove();
			svg = el("svg", { width, height, viewBox: `0 0 ${width} ${height}`, role: "img",
				"aria-label": options.label || "Ratings over time" });
			frame.prepend(svg);

			const grid = el("g", { class: "grid" }, svg);
			for (const v of ticks) {
				el("line", { x1: margin.left, x2: width - margin.right, y1: y(v), y2: y(v) }, grid);
				el("text", { x: margin.left - 6, y: y(v), dy: "0.32em", "text-anchor": "end" }, grid).textContent = v;
			}
			const tt = timeTicks(t0, t1, Math.max(2, Math.floor(width / 110)));
			for (const t of tt.ticks) {
				el("text", { x: x(t), y: height - 6, "text-anchor": "middle" }, grid).textContent =
					(tt.daily ? dateFormat : hourFormat).format(new Date(t * 1000));
			}

			lines.clear();
			// Context first, so that colored lines are on top
			const ordered = [...series.filter(s => !s.slot), ...series.filter(s => s.slot).reverse()];
			for (const s of ordered) {
				const g = el("g", { class: s.slot ? "series series-" + s.slot : "series context" }, svg);
				// Steps: a rating holds until the team's next match
				const d = s.points.map((p, i) => (i ? "H" + x(p.t).toFixed(1) + "V" : "M" + x(p.t).toFixed(1) + " ") + y(p.r).toFixed(1)).join("");
				el("path", { d, class: "line" }, g);
				if (options.versions) {
					for (const p of s.points.filter(p => p.v)) {
						const c = el("circle", { cx: x(p.t), cy: y(p.r), r: 4, class: "dot" }, g);
						el("title", {}, c).textContent = "Version " + p.v;
					}
				}
				if (s.slot) {
					const last = s.points[s.points.length - 1];
					el("circle", { cx: x(last.t), cy: y(last.r), r: 4, class: "dot" }, g);
				}
				lines.set(s.team, g);
			}
			cross = el("line", { class: "crosshair", y1: margin.top, y2: height - margin.bottom, visibility: "hidden" }, svg);
			applyFocus();
		}

		function applyFocus() {
			for (const [team, g] of lines) {
				g.classList.toggle("focus", team === focused);
				if (team === focused) svg.insertBefore(g, cross);
			}
			container.classList.toggle("has-focus", focused !== null);
		}

		function show(t) {
			cross.setAttribute("x1", x(t));
			cross.setAttribute("x2", x(t));
			cross.setAttribute("visibility", "visible");
			const rows = series.map(s => ({ s, r: valueAt(s.points, t) })).filter(row => row.r !== null)
				.sort((a, b) => b.r - a.r);
			tooltip.textContent = "";
			const head = document.createElement("div");
			head.className = "when";
			head.textContent = fullFormat.format(new Date(t * 1000));
			tooltip.append(head);
			for (const { s, r } of rows) {
				const row = document.createElement("div");
				if (s.team === focused) row.className = "focus";
				const key = document.createElement("i");
				key.className = "key " + (s.slot ? "series-" + s.slot : "context");
				const value = document.createElement("b");
				value.textContent = r.toFixed(1);
				const name = document.createElement("span");
				name.textContent = series.length > 1 ? s.team : "rating";
				row.append(key, value, name);
				tooltip.append(row);
			}
			tooltip.hidden = false;
			const left = x(t) + 12;
			tooltip.style.left = (left + tooltip.offsetWidth > frame.clientWidth ? x(t) - 12 - tooltip.offsetWidth : left) + "px";
			tooltip.style.top = margin.top + "px";
		}

		function hide() {
			cross.setAttribute("visibility", "hidden");
			tooltip.hidden = true;
		}

		// The nearest time with a match
		function nearest(px) {
			let best = times[0];
			for (const t of times) if (Math.abs(x(t) - px) < Math.abs(x(best) - px)) best = t;
			return best;
		}

		let current = null;
		frame.addEventListener("pointermove", e => {
			const rect = frame.getBoundingClientRect();
			current = nearest(e.clientX - rect.left);
			show(current);
		});
		frame.addEventListener("pointerleave", hide);
		frame.tabIndex = 0;
		frame.addEventListener("keydown", e => {
			if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
			let i = current === null ? times.length - 1 : times.indexOf(current) + (e.key === "ArrowRight" ? 1 : -1);
			current = times[Math.max(0, Math.min(times.length - 1, i))];
			show(current);
			e.preventDefault();
		});
		frame.addEventListener("blur", hide);

		for (const row of document.querySelectorAll("[data-team]")) {
			row.addEventListener("pointerenter", () => { focused = row.dataset.team; applyFocus(); });
			row.addEventListener("pointerleave", () => { focused = null; applyFocus(); });
		}

		draw();
		new ResizeObserver(() => { draw(); }).observe(frame);
	};
})();
