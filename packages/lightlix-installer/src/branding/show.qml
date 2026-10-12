// 설치 진행 화면의 슬라이드다. Calamares 슬라이드쇼 API 2 를 쓴다.
import QtQuick 2.0
import calamares.slideshow 1.0

Presentation {
    id: presentation

    Timer {
        interval: 8000
        running: presentation.activatedInCalamares
        repeat: true
        onTriggered: presentation.goToNextSlide()
    }

    Slide {
        Rectangle {
            anchors.fill: parent
            color: "#182036"
            Text {
                anchors.centerIn: parent
                width: parent.width * 0.8
                horizontalAlignment: Text.AlignHCenter
                wrapMode: Text.WordWrap
                color: "#E8EEF8"
                font.pixelSize: 26
                text: "Lightlix\nA small Debian-based system for running local language models."
            }
        }
    }

    Slide {
        Rectangle {
            anchors.fill: parent
            color: "#182036"
            Text {
                anchors.centerIn: parent
                width: parent.width * 0.8
                horizontalAlignment: Text.AlignHCenter
                wrapMode: Text.WordWrap
                color: "#E8EEF8"
                font.pixelSize: 22
                text: "Server mode keeps memory use low.\nThe desktop is optional and can be turned off with 'lightlix mode server'."
            }
        }
    }

    Slide {
        Rectangle {
            anchors.fill: parent
            color: "#182036"
            Text {
                anchors.centerIn: parent
                width: parent.width * 0.8
                horizontalAlignment: Text.AlignHCenter
                wrapMode: Text.WordWrap
                color: "#E8EEF8"
                font.pixelSize: 22
                text: "Updates install the kernel and the NVIDIA driver together.\nThe previous kernel stays available as a fallback."
            }
        }
    }
}
