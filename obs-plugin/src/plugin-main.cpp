#include <QMainWindow>
#include <QMetaObject>
#include <QRandomGenerator>
#include <QString>

#include <obs-frontend-api.h>
#include <obs-module.h>
#include <util/platform.h>

#include <plugin-support.h>

#include "dock.hpp"
#include "engine.hpp"

OBS_DECLARE_MODULE()
OBS_MODULE_USE_DEFAULT_LOCALE(PLUGIN_NAME, "en-US")

namespace {

const char *kListen = "127.0.0.1:19355";
const char *kLocalServer = "rtmp://127.0.0.1:19355/live";

struct Hotkey {
	const char *name;
	const char *text;
	const char *command;
	obs_hotkey_id id = OBS_INVALID_HOTKEY_ID;
};

struct Plugin {
	Engine *engine = nullptr;
	Dock *dock = nullptr;
	obs_data_t *cfg = nullptr;
	bool restartWhenIdle = false;
	Hotkey hotkeys[3] = {{"clipdelay.match", "Hotkey.StartMatch", "match"},
			     {"clipdelay.end", "Hotkey.EndMatch", "fin"},
			     {"clipdelay.live", "Hotkey.GoLive", "live"}};
};

Plugin plugin;

QString text(const char *key)
{
	return QString::fromUtf8(obs_module_text(key));
}

char *configPath()
{
	return obs_module_config_path("settings.json");
}

void saveConfig()
{
	char *path = configPath();
	char *dir = obs_module_config_path("");
	os_mkdirs(dir);
	obs_data_save_json_safe(plugin.cfg, path, "tmp", "bak");
	bfree(dir);
	bfree(path);
}

void loadConfig()
{
	char *path = configPath();
	plugin.cfg = obs_data_create_from_json_file_safe(path, "bak");
	bfree(path);
	if (!plugin.cfg)
		plugin.cfg = obs_data_create();
	obs_data_set_default_int(plugin.cfg, "match_minutes", 3);
	if (!*obs_data_get_string(plugin.cfg, "ingest_key")) {
		const quint64 a = QRandomGenerator::system()->generate64();
		const quint64 b = QRandomGenerator::system()->generate64();
		const QString key = QString("%1%2").arg(a, 16, 16, QChar('0')).arg(b, 16, 16, QChar('0'));
		obs_data_set_string(plugin.cfg, "ingest_key", key.toUtf8().constData());
		saveConfig();
	}
}

bool pointsAtEngine(obs_service_t *service)
{
	const char *server = obs_service_get_connect_info(service, OBS_SERVICE_CONNECT_INFO_SERVER_URL);
	return server && QString::fromUtf8(server) == kLocalServer;
}

// upstreamOf joins a service's server and key the way an RTMP client sends them.
QString upstreamOf(obs_service_t *service)
{
	const char *server = obs_service_get_connect_info(service, OBS_SERVICE_CONNECT_INFO_SERVER_URL);
	const char *key = obs_service_get_connect_info(service, OBS_SERVICE_CONNECT_INFO_STREAM_KEY);
	if (!server || !*server || !key || !*key)
		return QString();
	QString url = QString::fromUtf8(server);
	if (!url.endsWith('/'))
		url += '/';
	return url + QString::fromUtf8(key);
}

// savedUpstream rebuilds the platform service the user had before ClipDelay took its place.
QString savedUpstream()
{
	obs_data_t *orig = obs_data_get_obj(plugin.cfg, "original_service");
	if (!orig)
		return QString();
	obs_data_t *settings = obs_data_get_obj(orig, "settings");
	obs_service_t *svc = obs_service_create_private(obs_data_get_string(orig, "type"), "clipdelay-upstream", settings);
	const QString url = svc ? upstreamOf(svc) : QString();
	obs_service_release(svc);
	obs_data_release(settings);
	obs_data_release(orig);
	return url;
}

void startEngine()
{
	const QString upstream = savedUpstream();
	if (upstream.isEmpty()) {
		plugin.dock->showError(text("Error.NoUpstream"));
		return;
	}
	char *program = obs_module_file("clipdelay-engine.exe");
	if (!program) {
		plugin.dock->showError(text("Error.NoEngine"));
		return;
	}
	EngineConfig cfg;
	cfg.program = QString::fromUtf8(program);
	cfg.upstream = upstream;
	cfg.listen = kListen;
	cfg.ingestKey = QString::fromUtf8(obs_data_get_string(plugin.cfg, "ingest_key"));
	cfg.matchDelaySec = int(obs_data_get_int(plugin.cfg, "match_minutes")) * 60;
	bfree(program);
	plugin.dock->showError(QString());
	plugin.engine->start(cfg);
}

void setService(const char *type, obs_data_t *settings)
{
	obs_service_t *svc = obs_service_create(type, "default_service", settings, nullptr);
	obs_frontend_set_streaming_service(svc);
	obs_frontend_save_streaming_service();
	obs_service_release(svc);
}

// enable puts the engine between OBS and the platform: OBS streams to the engine, which forwards.
bool enable()
{
	obs_service_t *cur = obs_frontend_get_streaming_service();
	if (!cur)
		return false;
	if (!pointsAtEngine(cur)) {
		if (upstreamOf(cur).isEmpty()) {
			plugin.dock->showError(text("Error.NoService"));
			return false;
		}
		obs_data_t *orig = obs_data_create();
		obs_data_t *settings = obs_service_get_settings(cur);
		obs_data_set_string(orig, "type", obs_service_get_type(cur));
		obs_data_set_obj(orig, "settings", settings);
		obs_data_set_obj(plugin.cfg, "original_service", orig);
		obs_data_release(settings);
		obs_data_release(orig);

		obs_data_t *local = obs_data_create();
		obs_data_set_string(local, "server", kLocalServer);
		obs_data_set_string(local, "key", obs_data_get_string(plugin.cfg, "ingest_key"));
		setService("rtmp_custom", local);
		obs_data_release(local);
	}
	obs_data_set_bool(plugin.cfg, "enabled", true);
	saveConfig();
	startEngine();
	return true;
}

void disable()
{
	plugin.engine->stop();
	obs_data_t *orig = obs_data_get_obj(plugin.cfg, "original_service");
	if (orig) {
		obs_data_t *settings = obs_data_get_obj(orig, "settings");
		setService(obs_data_get_string(orig, "type"), settings);
		obs_data_release(settings);
		obs_data_release(orig);
	}
	obs_data_erase(plugin.cfg, "original_service");
	obs_data_set_bool(plugin.cfg, "enabled", false);
	saveConfig();
	plugin.dock->showError(QString());
}

// syncWithProfile reads the switch from the active profile: on only while its service is the engine.
void syncWithProfile()
{
	obs_service_t *cur = obs_frontend_get_streaming_service();
	const bool on = cur && pointsAtEngine(cur) && !savedUpstream().isEmpty();
	if (on && !plugin.engine->running())
		startEngine();
	if (!on)
		plugin.engine->stop();
	plugin.dock->setEnabledState(on);
}

void onToggle(bool on)
{
	const EngineStatus &s = plugin.engine->status();
	if (obs_frontend_streaming_active() || s.ingesting || !s.upstream.isEmpty()) {
		plugin.dock->setEnabledState(!on);
		return;
	}
	if (on && !enable()) {
		plugin.dock->setEnabledState(false);
		return;
	}
	if (!on)
		disable();
	plugin.dock->setEnabledState(on);
}

void onMinutes(int minutes)
{
	obs_data_set_int(plugin.cfg, "match_minutes", minutes);
	saveConfig();
	// The engine keeps only the history the match delay needs, so a new delay needs a new engine.
	plugin.restartWhenIdle = plugin.engine->running();
}

void onEngineStatus()
{
	const EngineStatus &s = plugin.engine->status();
	if (plugin.restartWhenIdle && s.running && !s.ingesting && s.upstream.isEmpty()) {
		plugin.restartWhenIdle = false;
		startEngine();
	}
}

void sendFromHotkey(const char *command)
{
	QMetaObject::invokeMethod(
		plugin.engine, [command]() { plugin.engine->send(command); }, Qt::QueuedConnection);
}

void onHotkey(void *data, obs_hotkey_id, obs_hotkey_t *, bool pressed)
{
	if (pressed)
		sendFromHotkey(static_cast<Hotkey *>(data)->command);
}

void onSave(obs_data_t *save, bool saving, void *)
{
	for (Hotkey &h : plugin.hotkeys) {
		if (saving) {
			obs_data_array_t *arr = obs_hotkey_save(h.id);
			obs_data_set_array(save, h.name, arr);
			obs_data_array_release(arr);
		} else {
			obs_data_array_t *arr = obs_data_get_array(save, h.name);
			obs_hotkey_load(h.id, arr);
			obs_data_array_release(arr);
		}
	}
}

void onEvent(enum obs_frontend_event event, void *)
{
	switch (event) {
	case OBS_FRONTEND_EVENT_FINISHED_LOADING:
	case OBS_FRONTEND_EVENT_PROFILE_CHANGED:
		syncWithProfile();
		break;
	case OBS_FRONTEND_EVENT_STREAMING_STARTING:
		plugin.dock->setStreaming(true);
		if (obs_data_get_bool(plugin.cfg, "enabled") && !plugin.engine->running())
			startEngine();
		break;
	case OBS_FRONTEND_EVENT_STREAMING_STOPPED:
		plugin.dock->setStreaming(false);
		break;
	case OBS_FRONTEND_EVENT_EXIT:
		plugin.engine->release();
		break;
	default:
		break;
	}
}

} // namespace

const char *obs_module_description(void)
{
	return obs_module_text("Description");
}

bool obs_module_load(void)
{
	loadConfig();
	auto *window = static_cast<QMainWindow *>(obs_frontend_get_main_window());
	plugin.engine = new Engine(window);
	plugin.dock = new Dock(plugin.engine, window);
	plugin.dock->setMatchMinutes(int(obs_data_get_int(plugin.cfg, "match_minutes")));
	QObject::connect(plugin.dock, &Dock::enableToggled, onToggle);
	QObject::connect(plugin.dock, &Dock::matchMinutesChanged, onMinutes);
	QObject::connect(plugin.dock, &Dock::startMatch, []() { plugin.engine->send("match"); });
	QObject::connect(plugin.dock, &Dock::endMatch, []() { plugin.engine->send("fin"); });
	QObject::connect(plugin.dock, &Dock::goLive, []() { plugin.engine->send("live"); });
	QObject::connect(plugin.engine, &Engine::statusChanged, onEngineStatus);
	obs_frontend_add_dock_by_id("clipdelay", "ClipDelay", plugin.dock);
	for (Hotkey &h : plugin.hotkeys)
		h.id = obs_hotkey_register_frontend(h.name, obs_module_text(h.text), onHotkey, &h);
	obs_frontend_add_save_callback(onSave, nullptr);
	obs_frontend_add_event_callback(onEvent, nullptr);
	obs_log(LOG_INFO, "loaded (version %s)", PLUGIN_VERSION);
	return true;
}

void obs_module_unload(void)
{
	obs_frontend_remove_event_callback(onEvent, nullptr);
	obs_frontend_remove_save_callback(onSave, nullptr);
	for (Hotkey &h : plugin.hotkeys)
		obs_hotkey_unregister(h.id);
	obs_data_release(plugin.cfg);
}
