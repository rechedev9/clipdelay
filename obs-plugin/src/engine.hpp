#pragma once

#include <QObject>
#include <QProcess>
#include <QString>
#include <QTimer>

// EngineStatus mirrors the engine's "json" command.
struct EngineStatus {
	bool running = false;
	QString phase;
	qint64 targetMs = 0;
	qint64 appliedMs = 0;
	qint64 readyInMs = 0;
	bool ingesting = false;
	qint64 bufferMB = 0;
	QString upstream;
	int reconnects = 0;
	QString lastError;
};

struct EngineConfig {
	QString program;
	QString upstream;
	QString listen;
	QString ingestKey;
	int matchDelaySec = 180;
};

// Engine runs clipdelay-engine.exe as a child process and talks to it over stdin and stdout.
class Engine : public QObject {
	Q_OBJECT

public:
	explicit Engine(QObject *parent = nullptr);

	void start(const EngineConfig &cfg);
	void stop();
	// release lets a busy engine finish sending the delayed tail after OBS quits.
	void release();
	bool running() const;
	void send(const QString &command);
	const EngineStatus &status() const { return st; }

signals:
	void statusChanged();

private:
	void readOutput();
	void parseStatus(const QByteArray &line);

	QProcess *proc = nullptr;
	QTimer poll;
	EngineStatus st;
};
