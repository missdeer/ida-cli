"""Trimmed broker package shipped with the IDA plugin.

Upstream broker/__init__.py imports the Python broker server
(BrokerClient / IDAHttpServer). ida-cli replaces that server with an embedded
Go broker, so the plugin process only needs the SQLite cache modules.
"""
