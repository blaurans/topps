// Suivi Turbo Attax.
//
// Un seul document de 423 cartes : le navigateur fait les filtres, les
// totaux et le tri. Le serveur ne reçoit que des quantités, en valeur
// absolue, ce qui rend les doubles-clics inoffensifs.

const $ = (sel) => document.querySelector(sel);

const state = {
	set: { name: '', goal_total: 0 },
	rarities: [],
	cards: [],
	byCode: new Map(),
	teams: [],
	subsets: [],
	// Deux notions distinctes : `selected` est la carte active au clavier
	// et porte le liseré dans la grille ; `preview` est ce que montre le
	// panneau. En mode Manquantes ils divergent : le panneau confirme la
	// carte qui vient d'être cochée alors que le liseré se pose déjà sur
	// la manquante suivante.
	selected: null,
	preview: null,
	order: [],
	filters: { q: '', team: '', subset: '', sort: 'number', missing: false, thumbs: false },
};

const RARITY_SHORT = { green: '/99', gold: '/50', orange: '/25', red: '/5', '1of1': '1/1' };

const esc = (s) =>
	String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);

const norm = (s) =>
	String(s ?? '')
		.normalize('NFD')
		.replace(/[\u0300-\u036f]/g, '')
		.toLowerCase()
		.trim();

/* ---------- session ---------- */

// Une session peut expirer en cours de saisie (le cookie a trente jours,
// mais une session fermée dans un autre onglet suffit à invalider la
// suivante). Plutôt que d'afficher une erreur opaque, on renvoie vers la
// page de connexion.
function goToLogin() {
	window.location.href = '/login';
}

async function fetchJSON(url, options) {
	const res = await fetch(url, options);
	if (res.status === 401) {
		goToLogin();
		throw new Error('session expirée');
	}
	if (!res.ok) throw new Error((await res.json().catch(() => ({}))).error || res.statusText);
	return res.json();
}
/* ---------- modèle ---------- */

const baseQty = (c) => c.qty.base || 0;
const owned = (c) => baseQty(c) > 0;
const dup = (c) => baseQty(c) > 1;
const variantCount = (c) => c.variants.filter((r) => r !== 'base' && (c.qty[r] || 0) > 0).length;

/* ---------- persistance des filtres ---------- */

const STORE_KEY = 'topps.filters.v1';

function saveFilters() {
	try {
		localStorage.setItem(STORE_KEY, JSON.stringify(state.filters));
	} catch { /* navigation privée : sans conséquence */ }
}

function loadFilters() {
	try {
		Object.assign(state.filters, JSON.parse(localStorage.getItem(STORE_KEY) || '{}'));
	} catch { /* on repart des défauts */ }
}

/* ---------- rendu ---------- */

function visibleCards() {
	const { q, team, subset, missing } = state.filters;
	const needle = norm(q);

	let out = state.cards.filter((c) => {
		if (missing && owned(c)) return false;
		if (team && c.team !== team) return false;
		if (subset && c.subset !== subset) return false;
		if (needle) {
			const hay = norm(`${c.code} ${c.name} ${c.team} ${c.subset}`);
			if (!hay.includes(needle)) return false;
		}
		return true;
	});

	const sort = state.filters.sort;
	if (sort === 'name') {
		out.sort((a, b) => norm(a.name).localeCompare(norm(b.name)) || numOf(a) - numOf(b));
	} else if (sort === 'missing') {
		out.sort((a, b) => Number(owned(a)) - Number(owned(b)) || numOf(a) - numOf(b));
	} else {
		out.sort((a, b) => numOf(a) - numOf(b));
	}
	return out;
}

const numOf = (c) => parseInt(c.code, 10) || 9999;

function cardHTML(c) {
	const b = baseQty(c);
	const classes = ['card'];
	if (c.in_goal) classes.push('goal');
	if (owned(c)) classes.push('owned');
	if (dup(c)) classes.push('dup');
	if (state.selected === c.code) classes.push('sel');
	if (c.thumb) classes.push('has-thumb');

	const chips = c.variants
		.filter((r) => r !== 'base')
		.map((r) => {
			const n = c.qty[r] || 0;
			return `<button type="button" class="chip${n ? ' on' : ''}" data-rarity="${esc(r)}"
			        title="${esc(rarityLabel(r))}">${RARITY_SHORT[r] || r}</button>`;
		})
		.join('');

	return `<article class="${classes.join(' ')}" data-code="${esc(c.code)}" tabindex="-1">
		${c.thumb ? `<img class="thumb" loading="lazy" src="/thumb/${encodeURIComponent(c.code)}.jpg" alt="">` : ''}
		<div class="code"><span class="no">${esc(c.code)}</span>${b ? `<span class="qty${dup(c) ? ' dup' : ''}">${b}</span>` : ''}</div>
		<div class="name" title="${esc(c.name || c.team)}">${esc(c.name || c.team || '—')}</div>
		<div class="sub" title="${esc(c.team ? c.team + ' · ' : '')}${esc(c.subset)}">${esc(c.subset)}</div>
		${chips ? `<div class="chips">${chips}</div>` : ''}
	</article>`;
}

function rarityLabel(code) {
	const r = state.rarities.find((x) => x.code === code);
	return r ? r.label : code;
}

function render() {
	const list = visibleCards();
	state.order = list.map((c) => c.code);
	$('#grid').innerHTML = list.map(cardHTML).join('');
	$('#empty').hidden = list.length > 0;
	document.body.classList.toggle('thumbs', state.filters.thumbs);
	renderStats();
	renderStatus(list);
}

function renderStats() {
	const goal = state.set.goal_total || 0;
	const have = state.cards.filter((c) => c.in_goal && owned(c)).length;
	const dupes = state.cards.reduce((n, c) => n + Math.max(0, baseQty(c) - 1), 0);
	const variants = state.cards.reduce((n, c) => n + variantCount(c), 0);
	const excl = state.cards.filter((c) => !c.in_goal && owned(c)).length;
	const exclTotal = state.cards.filter((c) => !c.in_goal).length;

	$('#have').textContent = have;
	$('#goal').textContent = goal;
	$('#pct').textContent = goal ? `${Math.round((have / goal) * 100)} %` : '—';
	$('#bar').style.width = goal ? `${(have / goal) * 100}%` : '0';

	$('#stat-duplicates').textContent = dupes;
	$('#stat-variants').textContent = variants;
	$('#stat-exclusives').textContent = excl;
	$('#stat-exclusives-total').textContent = exclTotal;
	for (const [id, n] of [['#pl-duplicates', dupes], ['#pl-variants', variants]]) {
		$(id).textContent = n > 1 ? 's' : '';
	}
}

function renderStatus(list) {
	const { q, team, subset, missing, sort } = state.filters;
	const bits = [`${list.length} / ${state.cards.length} cartes affichées`];
	if (q) bits.push(`« ${q} »`);
	if (team) bits.push(team);
	if (subset) bits.push(subset);
	if (missing) bits.push('manquantes seulement');
	if (sort === 'missing') bits.push('tri : manquantes d’abord');
	if (sort === 'name') bits.push('tri : par nom');
	$('#status').textContent = bits.join(' · ');
}

function flash(msg, isError = false) {
	const el = $('#status');
	const prev = el.textContent;
	el.textContent = msg;
	el.classList.toggle('error', isError);
	clearTimeout(flash.timer);
	flash.timer = setTimeout(() => {
		el.textContent = prev;
		el.classList.remove('error');
	}, 2200);
}

/* ---------- écritures ---------- */

// patchCard remplace une seule cellule. Renvoie false si la carte n'est
// plus dans le DOM, cas où il faut refaire la grille entière.
function patchCard(card) {
	const el = $(`.card[data-code="${CSS.escape(card.code)}"]`);
	if (!el) return false;
	const tmp = document.createElement('div');
	tmp.innerHTML = cardHTML(card);
	el.replaceWith(tmp.firstElementChild);
	return true;
}

// sync réaligne l'affichage après un changement. En mode Manquantes la
// carte cochée ne doit plus figurer dans la grille : c'est le filtre qui
// décide, pas la présence résiduelle dans le DOM.
function sync(card) {
	const horsFiltre = state.filters.missing && card.in_goal && owned(card);
	if (!horsFiltre && patchCard(card)) {
		renderStats();
		renderStatus(visibleCards());
	} else {
		render();
	}
	renderPreview();
}

async function setQty(card, rarity, qty) {
	const before = card.qty[rarity] || 0;
	card.qty[rarity] = qty;
	sync(card);

	try {
		await fetchJSON('/api/holding', {
			method: 'PUT',
			headers: { 'Content-Type': 'application/json' },
			body: JSON.stringify({ card: card.code, rarity, qty }),
		});
		sync(card);
	} catch (err) {
		card.qty[rarity] = before;
		sync(card);
		flash(`Échec de l'écriture : ${err.message}`, true);
	}
}

/**
 * Après un cochage. Le panneau garde la carte qui vient d'être cochée —
 * c'est la confirmation du clic — tandis que le liseré de la grille se
 * pose sur la manquante suivante, prête pour la suite de la saisie.
 */
function afterTick(card) {
	if (state.filters.missing && card.in_goal && owned(card)) {
		const next = nextMissing();
		select(next ? next.code : null, { preview: false });
	} else {
		select(card.code, { preview: false });
	}
	state.preview = card.code;
	renderPreview();
}

/* ---------- sélection ---------- */

// select pose le liseré de la grille et, sauf mention contraire,
// l'aperçu qui suit normalement la navigation.
function select(code, opts = {}) {
	const { scroll = true, preview = true } = opts;
	const prev = state.selected ? $(`.card[data-code="${CSS.escape(state.selected)}"]`) : null;
	state.selected = code;
	if (prev) prev.classList.remove('sel');
	const next = code ? $(`.card[data-code="${CSS.escape(code)}"]`) : null;
	if (next) {
		next.classList.add('sel');
		if (scroll) next.scrollIntoView({ block: 'nearest' });
	}
	if (preview) state.preview = code;
	renderPreview();
}

// Le panneau affiche en priorite la carte qu'on vient de cocher, sinon la
// carte active. Sans cette distinction, cocher une carte en mode
// Manquantes fait sauter l'apercu sur une autre, et l'on ne voit plus
// ce que l'on vient de cocher.
function renderPreview() {
	const card = state.byCode.get(state.preview || state.selected);
	const box = $('#preview');
	if (!card) {
		box.hidden = true;
		return;
	}
	box.hidden = false;

	const img = $('#preview-img');
	if (card.thumb) {
		const src = `/thumb/${encodeURIComponent(card.code)}.jpg`;
		if (img.getAttribute('src') !== src) img.src = src;
		img.hidden = false;
	} else {
		img.removeAttribute('src');
		img.hidden = true;
	}

	$('#p-code').textContent = card.code;
	const b = baseQty(card);
	const extra = card.variants
		.filter((r) => r !== 'base' && (card.qty[r] || 0) > 0)
		.map((r) => `${RARITY_SHORT[r] || r}×${card.qty[r]}`);
	$('#p-qty').textContent = [b ? `×${b}` : '', ...extra].join(' ');
	$('#p-name').textContent = card.name || card.team || '—';
	$('#p-team').textContent = card.team;
	$('#p-subset').textContent = card.subset;
}

/** Carte manquante suivante dans l'ordre courant, en boucle. */
function nextMissing() {
	const list = visibleCards().filter((c) => c.in_goal && !owned(c));
	if (!list.length) return null;
	const at = list.findIndex((c) => c.code === state.selected);
	return list[(at + 1) % list.length];
}

function bump(delta) {
	if (!state.selected) {
		flash('Aucune carte sélectionnée');
		return;
	}
	const card = state.byCode.get(state.selected);
	if (!card) return;
	setQty(card, 'base', Math.max(0, baseQty(card) + delta));
	afterTick(card);
}

/* ---------- surcharge clavier ---------- */

function goToNumber(value) {
	const n = parseInt(value.trim(), 10);
	if (!Number.isFinite(n)) return false;
	const card = state.cards.find((c) => c.code === String(n));
	if (!card) return false;
	state.filters.missing = false;
	$('#missing-only').checked = false;
	saveFilters();
	state.filters.q = '';
	$('#search').value = '';
	render();
	setQty(card, 'base', baseQty(card) + 1);
	afterTick(card);
	return true;
}

document.addEventListener('keydown', (e) => {
	const typing = ['INPUT', 'SELECT', 'TEXTAREA'].includes(e.target.tagName);

	if (typing) {
		if (e.key === 'Enter' && e.target.id === 'search') {
			e.preventDefault();
			if (!goToNumber(e.target.value)) render();
			e.target.blur();
		} else if (e.key === 'Escape') {
			e.target.blur();
		}
		return;
	}

	switch (e.key) {
		case '/':
			e.preventDefault();
			$('#search').focus();
			$('#search').select();
			break;
		case '+':
		case '=':
			e.preventDefault();
			bump(1);
			break;
		case '-':
			e.preventDefault();
			bump(-1);
			break;
		case 'Escape':
			select(null);
			break;
		case 'n': {
			e.preventDefault();
			const next = nextMissing();
			if (next) select(next.code);
			else flash('Toutes les cartes de l’objectif sont cochées');
			break;
		}
		default:
			if (/^[0-9]$/.test(e.key)) {
				e.preventDefault();
				const s = $('#search');
				s.value = e.key;
				s.focus();
				s.select();
			}
	}
});

/* ---------- interactions ---------- */

$('#grid').addEventListener('click', (e) => {
	const chip = e.target.closest('.chip');
	if (chip) {
		const card = state.byCode.get(chip.closest('.card').dataset.code);
		const rarity = chip.dataset.rarity;
		setQty(card, rarity, (card.qty[rarity] || 0) > 0 ? 0 : 1);
		afterTick(card);
		return;
	}
	const cell = e.target.closest('.card');
	if (!cell) return;
	const card = state.byCode.get(cell.dataset.code);
	setQty(card, 'base', baseQty(card) + 1);
	afterTick(card);
});

$('#grid').addEventListener('contextmenu', (e) => {
	const cell = e.target.closest('.card');
	if (!cell) return;
	e.preventDefault();
	const card = state.byCode.get(cell.dataset.code);
	setQty(card, 'base', Math.max(0, baseQty(card) - 1));
	afterTick(card);
});

/* ---------- filtres ---------- */

function fillSelect(sel, values, allLabel) {
	sel.innerHTML = `<option value="">${allLabel}</option>` +
		values.map((v) => `<option value="${esc(v)}">${esc(v)}</option>`).join('');
}

function bindFilter(id, key, after) {
	$(id).addEventListener('input', (e) => {
		const el = e.target;
		// Pour une case à cocher, .value vaut toujours "on" : c'est
		// .checked qui porte l'état, sinon impossible de décocher.
		state.filters[key] = el.type === 'checkbox' ? el.checked : el.value;
		saveFilters();
		render();
		if (after) after();
	});
}

// renderMe pose l'identité en haut de page. Sans elle, deux personnes
// partageant le même écran ne peuvent pas savoir à qui appartient ce qu
// elles voient.
function renderMe(me) {
	const label = me.name || me.email;
	$('#me-name').textContent = label;
	$('#me-name').title = me.email;
	if (me.picture) {
		const pic = $('#me-pic');
		pic.src = me.picture;
		pic.alt = '';
		pic.hidden = false;
	}
}

async function boot() {
	let me;
	let data;
	try {
		me = await fetchJSON('/api/me');
		data = await fetchJSON('/api/catalogue');
	} catch (err) {
		if (err.message !== 'session expirée') {
			$('#status').textContent = `Impossible de charger le catalogue (${err.message})`;
			$('#status').classList.add('error');
		}
		return;
	}
	renderMe(me);

	state.set = data.set;
	state.rarities = data.rarities;
	state.cards = data.cards;
	for (const c of state.cards) state.byCode.set(c.code, c);

	// Listes de filtres derivatives des données, triées.
	const counts = (field) => {
		const m = new Map();
		for (const c of state.cards) if (c[field]) m.set(c[field], (m.get(c[field]) || 0) + 1);
		return [...m.entries()].sort((a, b) => a[0].localeCompare(b[0]));
	};
	state.teams = counts('team').map(([v]) => v);
	state.subsets = counts('subset').map(([v]) => v);

	$('#set-name').textContent = state.set.name;
	$('#set-meta').textContent =
		`${state.set.publisher} · ${state.set.season} · ${state.cards.length} cartes au checklist`;

	fillSelect($('#filter-team'), state.teams, 'Toutes les écuries');
	fillSelect($('#filter-subset'), state.subsets, 'Toutes les séries');

	loadFilters();
	const f = state.filters;
	$('#search').value = f.q;
	$('#filter-team').value = f.team;
	$('#filter-subset').value = f.subset;
	$('#filter-sort').value = f.sort;
	$('#missing-only').checked = Boolean(f.missing);
	$('#with-thumbs').checked = Boolean(f.thumbs);

	bindFilter('#search', 'q');
	bindFilter('#filter-team', 'team');
	bindFilter('#filter-subset', 'subset');
	bindFilter('#filter-sort', 'sort');
	bindFilter('#missing-only', 'missing');
	bindFilter('#with-thumbs', 'thumbs');

	$('#reset').addEventListener('click', async () => {
		if (!confirm('Remettre toutes les quantités à zéro ?')) return;
		for (const c of state.cards) {
			for (const r of c.variants) {
				if (c.qty[r]) setQty(c, r, 0);
			}
		}
		render();
	});

	document.addEventListener('keydown', (e) => {
		if (['INPUT', 'SELECT', 'TEXTAREA'].includes(e.target.tagName)) return;
		if (e.key === 'm') { $('#missing-only').click(); select(null); }
		if (e.key === 'i') $('#with-thumbs').click();
	});

	render();
}

boot();
