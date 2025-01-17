
CXX = g++
CXXFLAGS = -std=c++17 -O2
TARGET = dump

all: $(TARGET)

$(TARGET): dump.cpp
	$(CXX) $(CXXFLAGS) dump.cpp -o $(TARGET)

clean:
	rm -f $(TARGET)
