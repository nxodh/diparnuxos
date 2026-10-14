"""gettext 설정. 메시지 ID 는 영어이며 한국어 번역은 po/ko.po 에 있다."""

import gettext
import os

DOMAIN = "opereta-control-center"
_DEFAULT_LOCALEDIR = "/usr/share/locale"

_translation = gettext.NullTranslations()


def setup(localedir=None, languages=None):
    """번역을 불러온다. 번역 파일이 없으면 영어 원문을 쓴다."""
    global _translation
    localedir = localedir or os.environ.get("OPERETA_CC_LOCALEDIR") or _DEFAULT_LOCALEDIR
    _translation = gettext.translation(DOMAIN, localedir=localedir, languages=languages, fallback=True)
    return _translation


def _(message):
    return _translation.gettext(message)


def ngettext(singular, plural, n):
    return _translation.ngettext(singular, plural, n)
