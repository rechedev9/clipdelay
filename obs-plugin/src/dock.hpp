// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 Luis Reche
#pragma once

#include <QCheckBox>
#include <QLabel>
#include <QPushButton>
#include <QSpinBox>
#include <QWidget>

#include "engine.hpp"

// Dock is the ClipDelay panel: the on/off switch, the match delay, the three actions and the status.
class Dock : public QWidget {
	Q_OBJECT

public:
	explicit Dock(Engine *engine, QWidget *parent = nullptr);

	void setEnabledState(bool on);
	void setMatchMinutes(int minutes);
	void setStreaming(bool streaming);
	void showError(const QString &message);

signals:
	void enableToggled(bool on);
	void matchMinutesChanged(int minutes);
	void startMatch();
	void endMatch();
	void goLive();

private:
	void refresh();

	Engine *engine;
	QCheckBox *enable;
	QSpinBox *minutes;
	QPushButton *matchBtn;
	QPushButton *endBtn;
	QPushButton *liveBtn;
	QLabel *state;
	QLabel *detail;
	QLabel *error;
	bool streaming = false;
};
