// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 Luis Reche
#include "dock.hpp"

#include <QHBoxLayout>
#include <QSignalBlocker>
#include <QVBoxLayout>

#include <obs-module.h>

static QString tr_(const char *key)
{
	return QString::fromUtf8(obs_module_text(key));
}

static QString clock(qint64 ms)
{
	const qint64 s = (ms + 500) / 1000;
	return QString("%1:%2").arg(s / 60).arg(s % 60, 2, 10, QChar('0'));
}

Dock::Dock(Engine *engine_, QWidget *parent) : QWidget(parent), engine(engine_)
{
	enable = new QCheckBox(tr_("Dock.Enable"));
	minutes = new QSpinBox();
	minutes->setRange(1, 10);
	minutes->setSuffix(" min");
	matchBtn = new QPushButton(tr_("Dock.StartMatch"));
	endBtn = new QPushButton(tr_("Dock.EndMatch"));
	liveBtn = new QPushButton(tr_("Dock.GoLive"));
	state = new QLabel();
	QFont big = state->font();
	big.setPointSizeF(big.pointSizeF() * 1.4);
	big.setBold(true);
	state->setFont(big);
	state->setWordWrap(true);
	detail = new QLabel();
	detail->setWordWrap(true);
	error = new QLabel();
	error->setWordWrap(true);
	error->setStyleSheet("color: #e5484d;");
	error->hide();

	auto *delayRow = new QHBoxLayout();
	delayRow->addWidget(new QLabel(tr_("Dock.MatchDelay")));
	delayRow->addWidget(minutes);
	delayRow->addStretch();
	auto *buttons = new QHBoxLayout();
	buttons->addWidget(matchBtn);
	buttons->addWidget(endBtn);
	auto *root = new QVBoxLayout(this);
	root->addWidget(enable);
	root->addLayout(delayRow);
	root->addWidget(state);
	root->addWidget(detail);
	root->addLayout(buttons);
	root->addWidget(liveBtn);
	root->addWidget(error);
	root->addStretch();

	connect(enable, &QCheckBox::toggled, this, &Dock::enableToggled);
	connect(minutes, &QSpinBox::valueChanged, this, &Dock::matchMinutesChanged);
	connect(matchBtn, &QPushButton::clicked, this, &Dock::startMatch);
	connect(endBtn, &QPushButton::clicked, this, &Dock::endMatch);
	connect(liveBtn, &QPushButton::clicked, this, &Dock::goLive);
	connect(engine, &Engine::statusChanged, this, &Dock::refresh);
	refresh();
}

void Dock::setEnabledState(bool on)
{
	const QSignalBlocker block(enable);
	enable->setChecked(on);
	refresh();
}

void Dock::setMatchMinutes(int value)
{
	const QSignalBlocker block(minutes);
	minutes->setValue(value);
}

void Dock::setStreaming(bool value)
{
	streaming = value;
	refresh();
}

void Dock::showError(const QString &message)
{
	error->setText(message);
	error->setVisible(!message.isEmpty());
}

void Dock::refresh()
{
	const EngineStatus &s = engine->status();
	const bool on = enable->isChecked();
	// The switch and the delay wait until OBS stopped and the engine sent the delayed tail.
	const bool busy = streaming || s.ingesting || !s.upstream.isEmpty();
	enable->setEnabled(!busy);
	minutes->setEnabled(!busy);
	matchBtn->setEnabled(on && s.running);
	endBtn->setEnabled(on && s.running);
	liveBtn->setEnabled(on && s.running);

	if (!on) {
		state->setText(tr_("State.Off"));
		detail->setText(tr_("State.OffDetail"));
		return;
	}
	if (!s.running) {
		state->setText(tr_("State.Stopped"));
		detail->setText(s.exitMessage);
		return;
	}
	if (!s.ingesting && s.upstream.isEmpty()) {
		state->setText(tr_("State.Waiting"));
		detail->setText(tr_("State.WaitingDetail"));
		return;
	}
	if (s.phase == "returning") {
		state->setText(tr_("State.Returning").arg(clock(s.appliedMs)));
	} else if (s.readyInMs > 0) {
		state->setText(tr_("State.Filling").arg(clock(s.targetMs), clock(s.readyInMs)));
	} else if (s.appliedMs < 1000) {
		state->setText(tr_("State.Live"));
	} else {
		state->setText(tr_("State.Delayed").arg(clock(s.appliedMs)));
	}
	QString up = s.upstream == "live" ? tr_("Upstream.Live") : tr_("Upstream.Connecting");
	if (s.upstream == "retrying")
		up = tr_("Upstream.Retrying").arg(s.reconnects);
	if (!s.ingesting)
		up += " · " + tr_("Upstream.Draining");
	detail->setText(up);
	if (s.upstream == "retrying" && !s.lastError.isEmpty())
		showError(s.lastError);
}
