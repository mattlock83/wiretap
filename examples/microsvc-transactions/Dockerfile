FROM python:3.12-alpine
COPY wiretap /usr/local/bin/wiretap
COPY example /example
WORKDIR /example
CMD ["python3", "accounts_mock.py"]
