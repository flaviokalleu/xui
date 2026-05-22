"""
Diagnóstico da API Xtream Codes - inspeciona estrutura JSON de filmes e séries.
Uso: python inspect_xtream.py
"""
import json
import sys
import urllib.request
import urllib.parse
from pathlib import Path

BASE_URL = "http://serxui.pbecnettv.com"
USERNAME = "Flavio123@DIGNOMARAVILHOSO"
PASSWORD = "tuzJGXnuT3"

OUT_DIR = Path(__file__).parent / "xtream_data"
OUT_DIR.mkdir(exist_ok=True)


def api(action: str, extra: dict = None) -> dict | list:
    params = {"username": USERNAME, "password": PASSWORD}
    if action:
        params["action"] = action
    if extra:
        params.update(extra)
    url = f"{BASE_URL}/player_api.php?" + urllib.parse.urlencode(params)
    print(f"  GET {url[:100]}...")
    req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
    with urllib.request.urlopen(req, timeout=30) as r:
        return json.loads(r.read())


def save(name: str, data):
    path = OUT_DIR / f"{name}.json"
    path.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"  Salvo: {path}")
    return data


def summarize_vod(streams: list) -> dict:
    sample = streams[:3]
    keys = list(streams[0].keys()) if streams else []
    has_tmdb = sum(1 for s in streams if s.get("tmdb") not in (None, "", "0", 0))
    cats = {}
    for s in streams:
        c = str(s.get("category_id", "?"))
        cats[c] = cats.get(c, 0) + 1
    return {
        "total": len(streams),
        "keys_available": keys,
        "with_tmdb_id": has_tmdb,
        "categories_distribution": dict(sorted(cats.items(), key=lambda x: -x[1])[:10]),
        "sample_entries": sample,
    }


def summarize_series(series_list: list) -> dict:
    sample = series_list[:3]
    keys = list(series_list[0].keys()) if series_list else []
    has_tmdb = sum(1 for s in series_list if s.get("tmdb") not in (None, "", "0", 0))
    return {
        "total": len(series_list),
        "keys_available": keys,
        "with_tmdb_id": has_tmdb,
        "sample_entries": sample,
    }


def main():
    print("=== Diagnóstico Xtream Codes ===\n")

    # 1. Informações do servidor
    print("[1] Info do servidor...")
    try:
        info = save("00_server_info", api(""))
        print(f"  Status: {info.get('user_info', {}).get('status', '?')}")
        print(f"  Expira: {info.get('user_info', {}).get('exp_date', '?')}")
    except Exception as e:
        print(f"  ERRO: {e}")

    # 2. Categorias VOD
    print("\n[2] Categorias VOD (filmes)...")
    try:
        vod_cats = save("01_vod_categories", api("get_vod_categories"))
        print(f"  Total categorias: {len(vod_cats)}")
        for c in vod_cats[:5]:
            print(f"    - [{c.get('category_id')}] {c.get('category_name')} ({c.get('parent_id', 0)})")
    except Exception as e:
        print(f"  ERRO: {e}")

    # 3. Categorias Séries
    print("\n[3] Categorias Séries...")
    try:
        series_cats = save("02_series_categories", api("get_series_categories"))
        print(f"  Total categorias: {len(series_cats)}")
        for c in series_cats[:5]:
            print(f"    - [{c.get('category_id')}] {c.get('category_name')}")
    except Exception as e:
        print(f"  ERRO: {e}")

    # 4. Categorias Live
    print("\n[4] Categorias Live (canais)...")
    try:
        live_cats = save("03_live_categories", api("get_live_categories"))
        print(f"  Total categorias: {len(live_cats)}")
    except Exception as e:
        print(f"  ERRO: {e}")

    # 5. Streams VOD (filmes)
    print("\n[5] Streams VOD (filmes) - amostra...")
    try:
        vod_streams = api("get_vod_streams")
        summary = summarize_vod(vod_streams)
        save("04_vod_summary", summary)
        save("04_vod_sample_10", vod_streams[:10])
        print(f"  Total filmes: {summary['total']}")
        print(f"  Com TMDB ID: {summary['with_tmdb_id']}")
        print(f"  Campos: {summary['keys_available']}")
        if vod_streams:
            s = vod_streams[0]
            print(f"\n  Exemplo de filme:")
            print(f"    stream_id: {s.get('stream_id')}")
            print(f"    name: {s.get('name')}")
            print(f"    tmdb: {s.get('tmdb')}")
            print(f"    genre: {s.get('genre')}")
            print(f"    release_date: {s.get('release_date') or s.get('added')}")
            print(f"    container_extension: {s.get('container_extension')}")
            stream_url = f"{BASE_URL}/movie/{USERNAME}/{PASSWORD}/{s.get('stream_id')}.{s.get('container_extension', 'mp4')}"
            print(f"    URL do stream: {stream_url}")
    except Exception as e:
        print(f"  ERRO: {e}")

    # 6. Séries
    print("\n[6] Séries - amostra...")
    try:
        series_list = api("get_series")
        summary_s = summarize_series(series_list)
        save("05_series_summary", summary_s)
        save("05_series_sample_10", series_list[:10])
        print(f"  Total séries: {summary_s['total']}")
        print(f"  Com TMDB ID: {summary_s['with_tmdb_id']}")
        print(f"  Campos: {summary_s['keys_available']}")
        if series_list:
            s = series_list[0]
            print(f"\n  Exemplo de série:")
            print(f"    series_id: {s.get('series_id')}")
            print(f"    name: {s.get('name')}")
            print(f"    tmdb: {s.get('tmdb')}")
            print(f"    genre: {s.get('genre')}")

            # 7. Episódios da primeira série
            print(f"\n[7] Episódios da série '{s.get('name')}'...")
            try:
                ep_info = save("06_series_episodes_sample",
                               api("get_series_info", {"series_id": s.get("series_id")}))
                seasons = ep_info.get("episodes", {})
                print(f"  Temporadas: {list(seasons.keys())}")
                for season_num, eps in list(seasons.items())[:2]:
                    print(f"  Temporada {season_num}: {len(eps)} episódios")
                    if eps:
                        ep = eps[0]
                        print(f"    Ep 1: id={ep.get('id')} title={ep.get('title')}")
                        print(f"          ext={ep.get('container_extension')}")
                        ep_url = f"{BASE_URL}/series/{USERNAME}/{PASSWORD}/{ep.get('id')}.{ep.get('container_extension', 'mp4')}"
                        print(f"          URL: {ep_url}")
            except Exception as e:
                print(f"  ERRO episódios: {e}")
    except Exception as e:
        print(f"  ERRO: {e}")

    # 8. Amostra do M3U plus (primeiras 50 linhas)
    print("\n[8] Amostra M3U plus (primeiras 100 linhas)...")
    try:
        m3u_url = f"{BASE_URL}/get.php?username={USERNAME}&password={PASSWORD}&type=m3u_plus&output=hls"
        req = urllib.request.Request(m3u_url, headers={"User-Agent": "VLC/3.0"})
        with urllib.request.urlopen(req, timeout=30) as r:
            lines = []
            for i, line in enumerate(r):
                if i >= 100:
                    break
                lines.append(line.decode("utf-8", errors="replace").rstrip())
        m3u_path = OUT_DIR / "07_m3u_sample.txt"
        m3u_path.write_text("\n".join(lines), encoding="utf-8")
        print(f"  Salvo: {m3u_path}")
        # Detectar padrões de URL
        url_patterns = {"live": 0, "movie": 0, "series": 0, "other": 0}
        for line in lines:
            if "/live/" in line:
                url_patterns["live"] += 1
            elif "/movie/" in line:
                url_patterns["movie"] += 1
            elif "/series/" in line:
                url_patterns["series"] += 1
            elif line.startswith("http"):
                url_patterns["other"] += 1
        print(f"  Padrões de URL detectados: {url_patterns}")
    except Exception as e:
        print(f"  ERRO: {e}")

    print(f"\n=== Concluído. Dados salvos em: {OUT_DIR} ===")


if __name__ == "__main__":
    main()
