#include "engine.hpp"

#include <QJsonDocument>
#include <QJsonObject>
#include <QProcessEnvironment>

#include <obs-module.h>
#include <plugin-support.h>

Engine::Engine(QObject *parent) : QObject(parent)
{
	poll.setInterval(1000);
	connect(&poll, &QTimer::timeout, this, [this]() { send("json"); });
}

void Engine::start(const EngineConfig &cfg)
{
	stop();
	proc = new QProcess(this);
	proc->setProcessChannelMode(QProcess::MergedChannels);
	// The stream key travels in the environment, never on the command line other processes can read.
	QProcessEnvironment env = QProcessEnvironment::systemEnvironment();
	env.insert("CLIPDELAY_UPSTREAM", cfg.upstream);
	proc->setProcessEnvironment(env);
	const int retain = cfg.matchDelaySec + 10;
	const QStringList args = {"-control",
				  "-listen",
				  cfg.listen,
				  "-ingest-key",
				  cfg.ingestKey,
				  "-match-delay",
				  QString("%1s").arg(cfg.matchDelaySec),
				  "-retain",
				  QString("%1s").arg(retain)};
	connect(proc, &QProcess::readyReadStandardOutput, this, &Engine::readOutput);
	connect(proc, &QProcess::finished, this, [this](int code) {
		obs_log(LOG_INFO, "engine exited with code %d", code);
		st = EngineStatus{};
		poll.stop();
		emit statusChanged();
	});
	proc->start(cfg.program, args);
	if (!proc->waitForStarted(5000)) {
		obs_log(LOG_ERROR, "engine did not start: %s", proc->errorString().toUtf8().constData());
		st.lastError = proc->errorString();
		emit statusChanged();
		return;
	}
	st.running = true;
	poll.start();
	emit statusChanged();
}

void Engine::stop()
{
	poll.stop();
	if (!proc)
		return;
	proc->disconnect(this);
	if (proc->state() != QProcess::NotRunning) {
		proc->write("quit\n");
		if (!proc->waitForFinished(3000))
			proc->kill();
	}
	delete proc;
	proc = nullptr;
	st = EngineStatus{};
	emit statusChanged();
}

void Engine::release()
{
	poll.stop();
	if (!proc)
		return;
	proc->disconnect(this);
	proc->closeWriteChannel();
	// Deleting a QProcess kills the child, so this one is left to exit on its own once it drained.
	proc->setParent(nullptr);
	proc = nullptr;
}

bool Engine::running() const
{
	return proc && proc->state() == QProcess::Running;
}

void Engine::send(const QString &command)
{
	if (command != "json")
		obs_log(LOG_INFO, "command: %s (engine %s)", command.toUtf8().constData(),
			running() ? "running" : "not running");
	if (running())
		proc->write((command + "\n").toUtf8());
}

void Engine::readOutput()
{
	while (proc && proc->canReadLine()) {
		const QByteArray line = proc->readLine().trimmed();
		if (line.startsWith('{')) {
			parseStatus(line);
		} else if (!line.isEmpty() && line != "ok") {
			obs_log(LOG_INFO, "engine: %s", line.constData());
		}
	}
}

void Engine::parseStatus(const QByteArray &line)
{
	const QJsonObject o = QJsonDocument::fromJson(line).object();
	if (o.isEmpty())
		return;
	st.running = true;
	st.phase = o["phase"].toString();
	st.targetMs = o["target_ms"].toInteger();
	st.appliedMs = o["applied_ms"].toInteger();
	st.readyInMs = o["ready_in_ms"].toInteger();
	st.ingesting = o["ingesting"].toBool();
	st.bufferMB = o["buffer_mb"].toInteger();
	st.upstream = o["upstream"].toString();
	st.reconnects = o["reconnects"].toInt();
	st.lastError = o["last_error"].toString();
	emit statusChanged();
}
